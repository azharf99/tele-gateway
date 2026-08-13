// internal/delivery/telegram/message_handler_test.go
package telegram

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// fakeAuctionUseCase records what the handler asked it to do. The embedded
// interface is nil on purpose: any method the bidding path is not supposed to
// touch panics loudly instead of silently passing.
type fakeAuctionUseCase struct {
	domain.AuctionUseCase

	rule *domain.BidRule

	mu           sync.Mutex
	keywordCalls int
	bids         []tg.InputPeerClass
	bidded       chan struct{}
}

func newFakeUseCase(rule *domain.BidRule) *fakeAuctionUseCase {
	return &fakeAuctionUseCase{rule: rule, bidded: make(chan struct{}, 8)}
}

func (f *fakeAuctionUseCase) CheckAndStopByText(context.Context, string, int64, int) error {
	return nil
}

func (f *fakeAuctionUseCase) CheckKeyword(string, int64, int) (*domain.BidRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keywordCalls++
	return f.rule, nil
}

func (f *fakeAuctionUseCase) ExecuteBid(_ context.Context, peer tg.InputPeerClass, _ int, _ *domain.BidRule) error {
	f.mu.Lock()
	f.bids = append(f.bids, peer)
	f.mu.Unlock()
	f.bidded <- struct{}{}
	return nil
}

func (f *fakeAuctionUseCase) snapshot() (int, []tg.InputPeerClass) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keywordCalls, append([]tg.InputPeerClass(nil), f.bids...)
}

func newTestHandler(uc domain.AuctionUseCase) *AuctionHandler {
	return &AuctionHandler{
		UseCase:  uc,
		Logger:   zap.NewNop(),
		BidDelay: func() time.Duration { return 0 },
	}
}

func channelMessage(channelID int64, msgID, topicID int) *tg.Message {
	msg := &tg.Message{
		ID:      msgID,
		PeerID:  &tg.PeerChannel{ChannelID: channelID},
		Message: "Jadwal: Senin 19.00 WIB",
	}
	if topicID > 0 {
		msg.ReplyTo = &tg.MessageReplyHeader{ForumTopic: true, ReplyToTopID: topicID}
	}
	return msg
}

func entitiesWithChannel(channel *tg.Channel) tg.Entities {
	return tg.Entities{
		Users:    map[int64]*tg.User{},
		Chats:    map[int64]*tg.Chat{},
		Channels: map[int64]*tg.Channel{channel.ID: channel},
	}
}

// waitForBid blocks until the handler's background bid goroutine has run.
func waitForBid(t *testing.T, f *fakeAuctionUseCase) {
	t.Helper()
	select {
	case <-f.bidded:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bid to be executed")
	}
}

// A "min" channel constructor carries an access hash that Telegram rejects on
// outgoing calls (CHANNEL_INVALID), which is what silently broke bidding in the
// forum group. The handler must not propagate it.
func TestOnNewMessageDropsMinChannelAccessHash(t *testing.T) {
	fake := newFakeUseCase(&domain.BidRule{Model: gorm.Model{ID: 1}, Keyword: "senin"})
	h := newTestHandler(fake)

	entities := entitiesWithChannel(&tg.Channel{ID: 3720761110, AccessHash: 123456789, Min: true})
	if err := h.OnNewMessage(context.Background(), entities, channelMessage(3720761110, 39658, 24)); err != nil {
		t.Fatalf("OnNewMessage: %v", err)
	}
	waitForBid(t, fake)

	_, bids := fake.snapshot()
	peer, ok := bids[0].(*tg.InputPeerChannel)
	if !ok {
		t.Fatalf("expected *tg.InputPeerChannel, got %T", bids[0])
	}
	if peer.ChannelID != 3720761110 {
		t.Errorf("ChannelID = %d, want 3720761110", peer.ChannelID)
	}
	if peer.AccessHash != 0 {
		t.Errorf("AccessHash = %d, want 0 so the client resolves a usable one", peer.AccessHash)
	}
}

func TestOnNewMessageKeepsFullChannelAccessHash(t *testing.T) {
	fake := newFakeUseCase(&domain.BidRule{Model: gorm.Model{ID: 1}, Keyword: "senin"})
	h := newTestHandler(fake)

	entities := entitiesWithChannel(&tg.Channel{ID: 3914303696, AccessHash: 987654321})
	if err := h.OnNewMessage(context.Background(), entities, channelMessage(3914303696, 25, 0)); err != nil {
		t.Fatalf("OnNewMessage: %v", err)
	}
	waitForBid(t, fake)

	_, bids := fake.snapshot()
	peer := bids[0].(*tg.InputPeerChannel)
	if peer.AccessHash != 987654321 {
		t.Errorf("AccessHash = %d, want 987654321 to be preserved", peer.AccessHash)
	}
}

// Telegram re-delivers the same message; without de-duplication one auction post
// produced two bids.
func TestOnNewMessageIgnoresDuplicateDelivery(t *testing.T) {
	fake := newFakeUseCase(&domain.BidRule{Model: gorm.Model{ID: 1}, Keyword: "senin"})
	h := newTestHandler(fake)

	entities := entitiesWithChannel(&tg.Channel{ID: 3720761110, AccessHash: 1})
	for range 3 {
		if err := h.OnNewMessage(context.Background(), entities, channelMessage(3720761110, 39658, 24)); err != nil {
			t.Fatalf("OnNewMessage: %v", err)
		}
	}
	waitForBid(t, fake)

	calls, bids := fake.snapshot()
	if calls != 1 {
		t.Errorf("CheckKeyword called %d times, want 1", calls)
	}
	if len(bids) != 1 {
		t.Errorf("executed %d bids, want 1", len(bids))
	}
}

// A different message in the same group is not a duplicate.
func TestOnNewMessageHandlesDistinctMessages(t *testing.T) {
	fake := newFakeUseCase(&domain.BidRule{Model: gorm.Model{ID: 1}, Keyword: "senin"})
	h := newTestHandler(fake)

	entities := entitiesWithChannel(&tg.Channel{ID: 3720761110, AccessHash: 1})
	for _, msgID := range []int{39658, 39659} {
		if err := h.OnNewMessage(context.Background(), entities, channelMessage(3720761110, msgID, 24)); err != nil {
			t.Fatalf("OnNewMessage: %v", err)
		}
		waitForBid(t, fake)
	}

	if calls, bids := fake.snapshot(); calls != 2 || len(bids) != 2 {
		t.Errorf("got %d keyword checks and %d bids, want 2 and 2", calls, len(bids))
	}
}

func TestIsStalePeerErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"channel invalid", tgerr.New(400, tg.ErrChannelInvalid), true},
		{"peer id invalid", tgerr.New(400, tg.ErrPeerIDInvalid), true},
		{"channel private", tgerr.New(400, tg.ErrChannelPrivate), true},
		{"flood wait is not a peer problem", tgerr.New(420, "FLOOD_WAIT_30"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStalePeerErr(tt.err); got != tt.want {
				t.Errorf("isStalePeerErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestNextDialogOffset(t *testing.T) {
	dialogs := []tg.DialogClass{
		&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 10}, TopMessage: 100},
		&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 20}, TopMessage: 200},
	}
	messages := []tg.MessageClass{
		&tg.Message{ID: 100, Date: 1700000000},
		&tg.Message{ID: 200, Date: 1700000500},
	}
	chats := []tg.ChatClass{
		&tg.Channel{ID: 20, AccessHash: 42},
	}

	req, ok := nextDialogOffset(dialogs, messages, nil, chats)
	if !ok {
		t.Fatal("nextDialogOffset returned ok=false, want an offset for the last dialog")
	}
	if req.OffsetID != 200 {
		t.Errorf("OffsetID = %d, want 200", req.OffsetID)
	}
	if req.OffsetDate != 1700000500 {
		t.Errorf("OffsetDate = %d, want 1700000500", req.OffsetDate)
	}
	peer, isChannel := req.OffsetPeer.(*tg.InputPeerChannel)
	if !isChannel || peer.ChannelID != 20 || peer.AccessHash != 42 {
		t.Errorf("OffsetPeer = %#v, want channel 20 with hash 42", req.OffsetPeer)
	}
}

func TestCacheChannelsSkipsMinConstructors(t *testing.T) {
	c := &TelegramClient{channelHashes: map[int64]int64{}}
	c.cacheChannels([]tg.ChatClass{
		&tg.Channel{ID: 1, AccessHash: 111},
		&tg.Channel{ID: 2, AccessHash: 222, Min: true},
		&tg.Channel{ID: 3, AccessHash: 0},
	})

	if hash, ok := c.cachedChannelHash(1); !ok || hash != 111 {
		t.Errorf("channel 1: got (%d, %v), want (111, true)", hash, ok)
	}
	if _, ok := c.cachedChannelHash(2); ok {
		t.Error("channel 2 is a min constructor and must not be cached")
	}
	if _, ok := c.cachedChannelHash(3); ok {
		t.Error("channel 3 has a zero access hash and must not be cached")
	}
}
