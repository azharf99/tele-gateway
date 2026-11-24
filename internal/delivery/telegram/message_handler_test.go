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
)

type recordedGroupMsg struct {
	accountID   uint
	groupID     int64
	topicID     int
	msgID       int
	isUserReply bool
	text        string
	peer        tg.InputPeerClass
}

type fakeClassAuctionUC struct {
	domain.ClassAuctionUseCase

	mu       sync.Mutex
	received []recordedGroupMsg
	ch       chan struct{}
}

func newFakeClassAuctionUC() *fakeClassAuctionUC {
	return &fakeClassAuctionUC{ch: make(chan struct{}, 16)}
}

func (f *fakeClassAuctionUC) HandleIncomingGroupMessage(_ context.Context, accountID uint, groupID int64, topicID int, msgID int, isUserReply bool, text string, peer tg.InputPeerClass) error {
	f.mu.Lock()
	f.received = append(f.received, recordedGroupMsg{
		accountID:   accountID,
		groupID:     groupID,
		topicID:     topicID,
		msgID:       msgID,
		isUserReply: isUserReply,
		text:        text,
		peer:        peer,
	})
	f.mu.Unlock()
	f.ch <- struct{}{}
	return nil
}

func (f *fakeClassAuctionUC) snapshot() []recordedGroupMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedGroupMsg(nil), f.received...)
}

func waitForAuctionMsg(t *testing.T, f *fakeClassAuctionUC) {
	t.Helper()
	select {
	case <-f.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for auction message dispatch")
	}
}

func channelMessage(channelID int64, msgID, topicID int) *tg.Message {
	msg := &tg.Message{
		ID:      msgID,
		PeerID:  &tg.PeerChannel{ChannelID: channelID},
		Message: "Need tutor UNITY\n(FRI 20.30, SAT 12.00)",
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

func TestOnNewMessageDropsMinChannelAccessHash(t *testing.T) {
	fake := newFakeClassAuctionUC()
	h := NewAccountMessageHandler(2, AccountFeatureFlags{EnableClassAuction: true}, fake, nil, nil, zap.NewNop())

	entities := entitiesWithChannel(&tg.Channel{ID: 3720761110, AccessHash: 123456789, Min: true})
	if err := h.OnNewMessage(context.Background(), entities, channelMessage(3720761110, 39658, 24)); err != nil {
		t.Fatalf("OnNewMessage: %v", err)
	}
	waitForAuctionMsg(t, fake)

	recs := fake.snapshot()
	peer, ok := recs[0].peer.(*tg.InputPeerChannel)
	if !ok {
		t.Fatalf("expected *tg.InputPeerChannel, got %T", recs[0].peer)
	}
	if peer.ChannelID != 3720761110 || peer.AccessHash != 0 {
		t.Errorf("peer = (%d, %d), want (3720761110, 0)", peer.ChannelID, peer.AccessHash)
	}
	if recs[0].topicID != 24 || recs[0].isUserReply {
		t.Errorf("topic=%d isUserReply=%v, want (24, false)", recs[0].topicID, recs[0].isUserReply)
	}
}

func TestOnNewMessageKeepsFullChannelAccessHash(t *testing.T) {
	fake := newFakeClassAuctionUC()
	h := NewAccountMessageHandler(2, AccountFeatureFlags{EnableClassAuction: true}, fake, nil, nil, zap.NewNop())

	entities := entitiesWithChannel(&tg.Channel{ID: 3914303696, AccessHash: 987654321})
	if err := h.OnNewMessage(context.Background(), entities, channelMessage(3914303696, 25, 0)); err != nil {
		t.Fatalf("OnNewMessage: %v", err)
	}
	waitForAuctionMsg(t, fake)

	recs := fake.snapshot()
	peer := recs[0].peer.(*tg.InputPeerChannel)
	if peer.AccessHash != 987654321 {
		t.Errorf("AccessHash = %d, want 987654321", peer.AccessHash)
	}
}

func TestOnNewMessageIgnoresDuplicateDeliveryAndRespectsFeatureFlags(t *testing.T) {
	fake := newFakeClassAuctionUC()
	h := NewAccountMessageHandler(2, AccountFeatureFlags{EnableClassAuction: true}, fake, nil, nil, zap.NewNop())

	entities := entitiesWithChannel(&tg.Channel{ID: 3720761110, AccessHash: 1})
	for range 3 {
		_ = h.OnNewMessage(context.Background(), entities, channelMessage(3720761110, 39658, 24))
	}
	waitForAuctionMsg(t, fake)
	time.Sleep(50 * time.Millisecond)

	if recs := fake.snapshot(); len(recs) != 1 {
		t.Fatalf("dispatched %d times, want 1", len(recs))
	}

	// Disable ClassAuction -> subsequent distinct messages must not be dispatched
	h.SetFlags(AccountFeatureFlags{EnableClassAuction: false})
	_ = h.OnNewMessage(context.Background(), entities, channelMessage(3720761110, 39659, 24))
	time.Sleep(50 * time.Millisecond)

	if recs := fake.snapshot(); len(recs) != 1 {
		t.Fatalf("disabled flag should block dispatch, got %d", len(recs))
	}
}

func TestExtractTopicAndReplyInfo(t *testing.T) {
	tests := []struct {
		name      string
		msg       *tg.Message
		wantTopic int
		wantReply bool
	}{
		{
			name:      "plain group message",
			msg:       &tg.Message{ID: 10},
			wantTopic: 0,
			wantReply: false,
		},
		{
			name: "non-forum reply to another message",
			msg: &tg.Message{
				ID:      11,
				ReplyTo: &tg.MessageReplyHeader{ForumTopic: false, ReplyToMsgID: 10},
			},
			wantTopic: 0,
			wantReply: true,
		},
		{
			name: "direct post in forum topic (ReplyToMsgID = topic root)",
			msg: &tg.Message{
				ID:      12,
				ReplyTo: &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 24, ReplyToTopID: 0},
			},
			wantTopic: 24,
			wantReply: false,
		},
		{
			name: "reply to another message inside forum topic",
			msg: &tg.Message{
				ID:      13,
				ReplyTo: &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 12, ReplyToTopID: 24},
			},
			wantTopic: 24,
			wantReply: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			topic, isReply := extractTopicAndReplyInfo(tc.msg)
			if topic != tc.wantTopic || isReply != tc.wantReply {
				t.Errorf("got (%d, %v), want (%d, %v)", topic, isReply, tc.wantTopic, tc.wantReply)
			}
		})
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
	if req.OffsetID != 200 || req.OffsetDate != 1700000500 {
		t.Errorf("offset = (%d, %d), want (200, 1700000500)", req.OffsetID, req.OffsetDate)
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
