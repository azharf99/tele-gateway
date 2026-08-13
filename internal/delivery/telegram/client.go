// internal/delivery/telegram/client.go
package telegram

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"
)

const (
	dialogPageSize = 100
	maxDialogPages = 10
)

type TelegramClient struct {
	Client *telegram.Client
	Sender *message.Sender
	Logger *zap.Logger

	// channelHashes caches access hashes that are known to be valid for outgoing
	// RPC calls, keyed by channel ID. Hashes attached to push updates are NOT
	// always usable — see resolveChannelPeer — so they never populate this map.
	hashMu        sync.RWMutex
	channelHashes map[int64]int64
}

func NewTelegramClient(appID int, appHash string, sessionPath string, handler telegram.UpdateHandler, logger *zap.Logger) (*TelegramClient, error) {
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{
			Path: sessionPath,
		},
		UpdateHandler: handler,
	})

	return &TelegramClient{
		Client:        client,
		Sender:        message.NewSender(tg.NewClient(client)),
		Logger:        logger,
		channelHashes: make(map[int64]int64),
	}, nil
}

func (c *TelegramClient) Start(ctx context.Context, phone, password string, logger *zap.Logger, otpProvider func(context.Context) (string, error), onSuccess func()) error {
	flow := auth.NewFlow(
		auth.Constant(phone, password, auth.CodeAuthenticatorFunc(func(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
			logger.Info("Waiting for OTP...")
			if otpProvider != nil {
				code, err := otpProvider(ctx)
				if err == nil {
					return code, nil
				}
				logger.Warn("Failed to get OTP from provider, falling back to Console", zap.Error(err))
			}

			fmt.Print("Enter code (Console Fallback): ")
			code, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			return strings.TrimSpace(code), nil
		})),
		auth.SendCodeOptions{},
	)

	return c.Client.Run(ctx, func(ctx context.Context) error {
		status, err := c.Client.Auth().Status(ctx)
		if err == nil && status.Authorized {
			onSuccess()
			logger.Info("Bot is authorized", zap.String("username", status.User.Username), zap.Int64("id", status.User.ID))
		} else {
			logger.Info("Awaiting login/OTP")
		}

		if err := c.Client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}

		// Wajib memanggil UpdatesGetState agar server Telegram mulai mengirimkan push updates ke client ini
		if _, err := tg.NewClient(c.Client).UpdatesGetState(ctx); err != nil {
			logger.Warn("Failed to fetch initial updates state", zap.Error(err))
		}

		// Warm the access-hash cache so the very first bid of a session does not
		// have to pay for a dialog scan (and cannot fail on a stale hash).
		if err := c.refreshChannelHashes(ctx); err != nil {
			logger.Warn("Failed to warm channel access hash cache", zap.Error(err))
		}

		onSuccess()
		logger.Info("Userbot is running...")
		<-ctx.Done()
		return nil
	})
}

// Implementasi interface TelegramService yang ada di usecase
//
// Sending is peer-hash aware. An *tg.InputPeerChannel built from the entities of
// a push update can carry a "min" access hash, which Telegram rejects on
// outgoing calls with CHANNEL_INVALID even though the same account happily
// receives messages from that channel. Reply therefore prefers a hash resolved
// from the dialog list, and on a peer error it drops the cached hash, resolves a
// fresh one and retries once.
func (c *TelegramClient) Reply(ctx context.Context, peer tg.InputPeerClass, msgID int, text string) error {
	peer = c.usablePeer(ctx, peer)

	err := c.send(ctx, peer, msgID, text)
	if err == nil {
		return nil
	}

	channel, isChannel := peer.(*tg.InputPeerChannel)
	if !isChannel || !isStalePeerErr(err) {
		return fmt.Errorf("failed to send reply: %w", err)
	}

	c.forgetChannelHash(channel.ChannelID)
	fresh, resolveErr := c.resolveChannelPeer(ctx, channel.ChannelID)
	if resolveErr != nil {
		return fmt.Errorf("failed to send reply: %w (peer re-resolve failed: %v)", err, resolveErr)
	}

	if c.Logger != nil {
		c.Logger.Info("Re-resolved channel peer after rejected access hash",
			zap.Int64("channel_id", channel.ChannelID),
			zap.Error(err))
	}

	if retryErr := c.send(ctx, fresh, msgID, text); retryErr != nil {
		return fmt.Errorf("failed to send reply after peer re-resolve: %w", retryErr)
	}
	return nil
}

func (c *TelegramClient) send(ctx context.Context, peer tg.InputPeerClass, msgID int, text string) error {
	builder := c.Sender.To(peer).CloneBuilder()
	if msgID > 0 {
		builder = builder.Reply(msgID)
	}
	_, err := builder.Text(ctx, text)
	return err
}

// isStalePeerErr reports whether an RPC error means "the peer you addressed is
// not usable", which is recoverable by resolving the peer again.
func isStalePeerErr(err error) bool {
	return tgerr.Is(err, tg.ErrChannelInvalid, tg.ErrPeerIDInvalid, tg.ErrChannelPrivate)
}

// usablePeer swaps a channel peer's access hash for a cached, known-good one.
// Non-channel peers and channels we have never resolved are returned untouched
// so the normal send path still works; the retry in Reply covers the rest.
func (c *TelegramClient) usablePeer(ctx context.Context, peer tg.InputPeerClass) tg.InputPeerClass {
	channel, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return peer
	}

	if hash, cached := c.cachedChannelHash(channel.ChannelID); cached {
		return &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: hash}
	}

	// A zero hash can never work, so it is worth resolving up front. A non-zero
	// (possibly "min") hash is given the benefit of the doubt to save an RPC.
	if channel.AccessHash == 0 {
		if resolved, err := c.resolveChannelPeer(ctx, channel.ChannelID); err == nil {
			return resolved
		} else if c.Logger != nil {
			c.Logger.Warn("Failed to resolve channel peer before sending",
				zap.Int64("channel_id", channel.ChannelID), zap.Error(err))
		}
	}

	return peer
}

func (c *TelegramClient) cachedChannelHash(channelID int64) (int64, bool) {
	c.hashMu.RLock()
	defer c.hashMu.RUnlock()
	hash, ok := c.channelHashes[channelID]
	return hash, ok
}

func (c *TelegramClient) forgetChannelHash(channelID int64) {
	c.hashMu.Lock()
	defer c.hashMu.Unlock()
	delete(c.channelHashes, channelID)
}

// cacheChannels records the access hash of every full channel constructor in
// chats. "min" constructors are skipped: their access hash only works in the
// context it arrived in and would poison the cache.
func (c *TelegramClient) cacheChannels(chats []tg.ChatClass) {
	c.hashMu.Lock()
	defer c.hashMu.Unlock()

	if c.channelHashes == nil {
		c.channelHashes = make(map[int64]int64)
	}
	for _, chatClass := range chats {
		channel, ok := chatClass.(*tg.Channel)
		if !ok || channel.Min || channel.AccessHash == 0 {
			continue
		}
		c.channelHashes[channel.ID] = channel.AccessHash
	}
}

// resolveChannelPeer returns an input peer whose access hash is valid for
// outgoing RPC calls, scanning the dialog list if the channel is not cached yet.
func (c *TelegramClient) resolveChannelPeer(ctx context.Context, channelID int64) (*tg.InputPeerChannel, error) {
	if hash, ok := c.cachedChannelHash(channelID); ok {
		return &tg.InputPeerChannel{ChannelID: channelID, AccessHash: hash}, nil
	}

	if err := c.refreshChannelHashes(ctx); err != nil {
		return nil, err
	}

	if hash, ok := c.cachedChannelHash(channelID); ok {
		return &tg.InputPeerChannel{ChannelID: channelID, AccessHash: hash}, nil
	}

	return nil, fmt.Errorf("channel %d not found in dialogs", channelID)
}

func (c *TelegramClient) refreshChannelHashes(ctx context.Context) error {
	_, err := c.fetchDialogChats(ctx)
	return err
}

// fetchDialogChats walks the dialog list and returns every chat/channel it saw,
// caching channel access hashes along the way. Unlike push updates, dialogs
// always carry full constructors, which is what makes them a trustworthy source
// of access hashes.
func (c *TelegramClient) fetchDialogChats(ctx context.Context) ([]tg.ChatClass, error) {
	raw := tg.NewClient(c.Client)

	req := &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      dialogPageSize,
	}

	var (
		all  []tg.ChatClass
		seen = make(map[int64]struct{})
	)

	for range maxDialogPages {
		resp, err := raw.MessagesGetDialogs(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("telegram rpc error: %w", err)
		}

		var (
			dialogs  []tg.DialogClass
			messages []tg.MessageClass
			users    []tg.UserClass
			chats    []tg.ChatClass
			partial  bool
		)

		switch v := resp.(type) {
		case *tg.MessagesDialogs:
			dialogs, messages, users, chats = v.Dialogs, v.Messages, v.Users, v.Chats
		case *tg.MessagesDialogsSlice:
			dialogs, messages, users, chats = v.Dialogs, v.Messages, v.Users, v.Chats
			partial = true
		case *tg.MessagesDialogsNotModified:
			return all, nil
		default:
			return nil, fmt.Errorf("unexpected telegram response type: %T", resp)
		}

		c.cacheChannels(chats)
		for _, chatClass := range chats {
			id, ok := chatID(chatClass)
			if !ok {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			all = append(all, chatClass)
		}

		// A full messages.dialogs response is the complete list; a short page
		// means we reached the end of a slice.
		if !partial || len(dialogs) < dialogPageSize {
			return all, nil
		}

		next, ok := nextDialogOffset(dialogs, messages, users, chats)
		if !ok {
			return all, nil
		}
		req = next
	}

	return all, nil
}

func chatID(chatClass tg.ChatClass) (int64, bool) {
	switch chat := chatClass.(type) {
	case *tg.Chat:
		return chat.ID, true
	case *tg.Channel:
		return chat.ID, true
	}
	return 0, false
}

// nextDialogOffset builds the pagination request for the page after the one
// described by dialogs, following the offset_date/offset_id/offset_peer triple
// that messages.getDialogs expects.
func nextDialogOffset(dialogs []tg.DialogClass, messages []tg.MessageClass, users []tg.UserClass, chats []tg.ChatClass) (*tg.MessagesGetDialogsRequest, bool) {
	if len(dialogs) == 0 {
		return nil, false
	}

	last, ok := dialogs[len(dialogs)-1].(*tg.Dialog)
	if !ok {
		return nil, false
	}

	peer, ok := inputPeerFor(last.Peer, users, chats)
	if !ok {
		return nil, false
	}

	date := 0
	for _, msgClass := range messages {
		if msg, ok := msgClass.(*tg.Message); ok && msg.ID == last.TopMessage {
			date = msg.Date
			break
		}
	}
	if date == 0 {
		return nil, false
	}

	return &tg.MessagesGetDialogsRequest{
		OffsetDate: date,
		OffsetID:   last.TopMessage,
		OffsetPeer: peer,
		Limit:      dialogPageSize,
	}, true
}

func inputPeerFor(peerClass tg.PeerClass, users []tg.UserClass, chats []tg.ChatClass) (tg.InputPeerClass, bool) {
	switch peer := peerClass.(type) {
	case *tg.PeerUser:
		for _, userClass := range users {
			if user, ok := userClass.(*tg.User); ok && user.ID == peer.UserID {
				return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, true
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: peer.ChatID}, true
	case *tg.PeerChannel:
		for _, chatClass := range chats {
			if channel, ok := chatClass.(*tg.Channel); ok && channel.ID == peer.ChannelID {
				return &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, true
			}
		}
	}
	return nil, false
}

func (c *TelegramClient) GetGroups(ctx context.Context) ([]domain.GroupInfo, error) {
	chats, err := c.fetchDialogChats(ctx)
	if err != nil {
		return nil, err
	}

	var groups []domain.GroupInfo
	for _, chatClass := range chats {
		switch chat := chatClass.(type) {
		case *tg.Chat:
			if chat.Deactivated {
				continue
			}
			groups = append(groups, domain.GroupInfo{
				ID:    chat.ID,
				Title: chat.Title,
				Type:  "group",
			})
		case *tg.Channel:
			typeName := "channel"
			if chat.Megagroup {
				typeName = "supergroup"
			}
			groups = append(groups, domain.GroupInfo{
				ID:    chat.ID,
				Title: chat.Title,
				Type:  typeName,
			})
		}
	}

	return groups, nil
}

func (c *TelegramClient) GetTopics(ctx context.Context, groupID int64) ([]domain.TopicInfo, error) {
	generalOnly := []domain.TopicInfo{{ID: 0, Title: "General"}}

	chats, err := c.fetchDialogChats(ctx)
	if err != nil {
		return nil, err
	}

	var channel *tg.Channel
	for _, chatClass := range chats {
		if ch, ok := chatClass.(*tg.Channel); ok && ch.ID == groupID {
			channel = ch
			break
		}
	}

	switch {
	case channel == nil:
		// Basic groups have no forum topics; anything else is genuinely unknown.
		for _, chatClass := range chats {
			if chat, ok := chatClass.(*tg.Chat); ok && chat.ID == groupID {
				return generalOnly, nil
			}
		}
		return nil, fmt.Errorf("group %d not found in dialogs", groupID)
	case !channel.Megagroup || !channel.Forum:
		// Broadcast channels and topic-less supergroups only have the general thread.
		return generalOnly, nil
	}

	peer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}

	raw := tg.NewClient(c.Client)
	resp, err := raw.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
		Peer:        peer,
		OffsetDate:  0,
		OffsetID:    0,
		OffsetTopic: 0,
		Limit:       100,
	})
	if err != nil {
		if tgerr.Is(err, "CHANNEL_FORUM_MISSING", tg.ErrPeerIDInvalid) {
			return generalOnly, nil
		}
		return nil, fmt.Errorf("failed to get forum topics: %w", err)
	}

	topics := make([]domain.TopicInfo, 0, len(resp.Topics))
	for _, topicClass := range resp.Topics {
		if topic, ok := topicClass.(*tg.ForumTopic); ok {
			topics = append(topics, domain.TopicInfo{
				ID:    topic.ID,
				Title: topic.Title,
			})
		}
	}

	return topics, nil
}
