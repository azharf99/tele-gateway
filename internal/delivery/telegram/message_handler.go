// internal/delivery/telegram/message_handler.go
package telegram

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// seenMessageTTL is how long a (peer, message) pair is remembered for
// de-duplication so Telegram re-deliveries in multiple envelopes never double-fire.
const seenMessageTTL = 5 * time.Minute

type AccountFeatureFlags struct {
	EnableAIGateway    bool
	AISystemPrompt     string
	EnableClassAuction bool
}

type AccountMessageHandler struct {
	AccountID uint
	AuctionUC domain.ClassAuctionUseCase
	AIUC      domain.AIGatewayUseCase
	ReplyFunc func(ctx context.Context, peer tg.InputPeerClass, msgID int, text string) error
	Logger    *zap.Logger

	flagMu sync.RWMutex
	flags  AccountFeatureFlags

	seenMu   sync.Mutex
	seenMsgs map[string]time.Time
}

func NewAccountMessageHandler(
	accountID uint,
	flags AccountFeatureFlags,
	auctionUC domain.ClassAuctionUseCase,
	aiUC domain.AIGatewayUseCase,
	replyFunc func(ctx context.Context, peer tg.InputPeerClass, msgID int, text string) error,
	logger *zap.Logger,
) *AccountMessageHandler {
	return &AccountMessageHandler{
		AccountID: accountID,
		AuctionUC: auctionUC,
		AIUC:      aiUC,
		ReplyFunc: replyFunc,
		Logger:    logger,
		flags:     flags,
		seenMsgs:  make(map[string]time.Time),
	}
}

func (h *AccountMessageHandler) SetFlags(flags AccountFeatureFlags) {
	h.flagMu.Lock()
	h.flags = flags
	h.flagMu.Unlock()
}

func (h *AccountMessageHandler) getFlags() AccountFeatureFlags {
	h.flagMu.RLock()
	defer h.flagMu.RUnlock()
	return h.flags
}

// alreadyHandled reports whether this exact (peer, message) pair has been
// processed recently, and records it otherwise.
func (h *AccountMessageHandler) alreadyHandled(peerID int64, msgID int) bool {
	key := fmt.Sprintf("%d:%d", peerID, msgID)
	now := time.Now()

	h.seenMu.Lock()
	defer h.seenMu.Unlock()

	if h.seenMsgs == nil {
		h.seenMsgs = make(map[string]time.Time)
	}

	if seenAt, ok := h.seenMsgs[key]; ok && now.Sub(seenAt) < seenMessageTTL {
		return true
	}

	for k, at := range h.seenMsgs {
		if now.Sub(at) >= seenMessageTTL {
			delete(h.seenMsgs, k)
		}
	}

	h.seenMsgs[key] = now
	return false
}

func (h *AccountMessageHandler) OnNewMessage(ctx context.Context, entities tg.Entities, msg *tg.Message) error {
	if msg == nil || msg.Out {
		return nil
	}

	text := msg.Message
	topicID, isUserReply := extractTopicAndReplyInfo(msg)

	var (
		peer       tg.InputPeerClass
		peerID     int64
		isPrivate  bool
		senderName string
	)

	switch p := msg.PeerID.(type) {
	case *tg.PeerUser:
		isPrivate = true
		peerID = p.UserID
		if user, ok := entities.Users[p.UserID]; ok {
			senderName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
			peer = &tg.InputPeerUser{
				UserID:     user.ID,
				AccessHash: user.AccessHash,
			}
		} else {
			senderName = "User"
			peer = &tg.InputPeerUser{
				UserID:     p.UserID,
				AccessHash: 0,
			}
		}
	case *tg.PeerChat:
		peerID = p.ChatID
		peer = &tg.InputPeerChat{
			ChatID: p.ChatID,
		}
	case *tg.PeerChannel:
		peerID = p.ChannelID

		// Only a full channel constructor carries an access hash Telegram accepts on
		// outgoing calls. Leave AccessHash=0 for min/missing channels so client resolves it.
		var accessHash int64
		if channel, ok := entities.Channels[p.ChannelID]; ok && !channel.Min {
			accessHash = channel.AccessHash
		}

		peer = &tg.InputPeerChannel{
			ChannelID:  p.ChannelID,
			AccessHash: accessHash,
		}
	}

	if peer == nil {
		return nil
	}

	if h.alreadyHandled(peerID, msg.ID) {
		return nil
	}

	flags := h.getFlags()

	// Route private messages to AI Gateway only if enabled on this account.
	if isPrivate {
		if !flags.EnableAIGateway || h.AIUC == nil {
			return nil
		}
		replyFn := func(replyText string) error {
			if h.ReplyFunc == nil {
				return fmt.Errorf("reply func not wired")
			}
			return h.ReplyFunc(context.Background(), peer, msg.ID, replyText)
		}
		promptOverride := flags.AISystemPrompt
		go func() {
			if err := h.AIUC.HandlePrivateMessage(context.Background(), h.AccountID, peerID, senderName, promptOverride, text, replyFn); err != nil && h.Logger != nil {
				h.Logger.Error("Failed to handle private message in AI Gateway",
					zap.Uint("account_id", h.AccountID),
					zap.Error(err))
			}
		}()
		return nil
	}

	// Route group/channel messages to zero-delay Class Auction engine if enabled on this account.
	if !flags.EnableClassAuction || h.AuctionUC == nil {
		return nil
	}

	// Dispatch immediately in a goroutine so the MTProto update loop never blocks.
	// Delay is controlled inside ClassAuctionUseCase (defaults to 0ms for instant reply).
	go func(gID int64, tID int, mID int, reply bool, body string, p tg.InputPeerClass) {
		if err := h.AuctionUC.HandleIncomingGroupMessage(context.Background(), h.AccountID, gID, tID, mID, reply, body, p); err != nil && h.Logger != nil {
			h.Logger.Error("Class auction handler error",
				zap.Uint("account_id", h.AccountID),
				zap.Int64("group_id", gID),
				zap.Int("msg_id", mID),
				zap.Error(err))
		}
	}(peerID, topicID, msg.ID, isUserReply, text, peer)

	return nil
}

// extractTopicAndReplyInfo extracts the forum topic ID (0 for general/non-forum)
// and distinguishes a top-level forum topic post from a reply to another user's message.
func extractTopicAndReplyInfo(msg *tg.Message) (topicID int, isUserReply bool) {
	if msg == nil || msg.ReplyTo == nil {
		return 0, false
	}

	header, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
	if !ok {
		return 0, false
	}

	if header.ForumTopic {
		if header.ReplyToTopID > 0 {
			topicID = header.ReplyToTopID
			// In a forum topic, replying to another specific message sets ReplyToMsgID != ReplyToTopID.
			if header.ReplyToMsgID > 0 && header.ReplyToMsgID != header.ReplyToTopID {
				isUserReply = true
			}
			return topicID, isUserReply
		}
		if header.ReplyToMsgID > 0 {
			// Direct top-level message inside a forum topic (ReplyToMsgID is the topic root ID).
			return header.ReplyToMsgID, false
		}
		return 0, false
	}

	// Non-forum group/chat: any ReplyToMsgID > 0 is a reply to another message.
	if header.ReplyToMsgID > 0 {
		return 0, true
	}
	return 0, false
}

// Handle implements gotd's telegram.UpdateHandler.
// Intentionally ignores UpdateEditMessage / UpdateEditChannelMessage so CS edits
// (e.g. appending "SOLD") never trigger a duplicate bid.
func (h *AccountMessageHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	switch updates := u.(type) {
	case *tg.UpdateShortMessage:
		if updates.Out {
			return nil
		}
		if h.alreadyHandled(updates.UserID, updates.ID) {
			return nil
		}
		flags := h.getFlags()
		if !flags.EnableAIGateway || h.AIUC == nil {
			return nil
		}
		peer := &tg.InputPeerUser{UserID: updates.UserID}
		replyFn := func(replyText string) error {
			if h.ReplyFunc == nil {
				return fmt.Errorf("reply func not wired")
			}
			return h.ReplyFunc(context.Background(), peer, updates.ID, replyText)
		}
		promptOverride := flags.AISystemPrompt
		go func() {
			if err := h.AIUC.HandlePrivateMessage(context.Background(), h.AccountID, updates.UserID, "User", promptOverride, updates.Message, replyFn); err != nil && h.Logger != nil {
				h.Logger.Error("Failed to handle short private message in AI Gateway",
					zap.Uint("account_id", h.AccountID),
					zap.Error(err))
			}
		}()

	case *tg.Updates:
		entities := buildEntities(updates.GetUsers(), updates.GetChats())
		for _, update := range updates.Updates {
			h.dispatchUpdate(ctx, entities, update)
		}

	case *tg.UpdatesCombined:
		entities := buildEntities(updates.GetUsers(), updates.GetChats())
		for _, update := range updates.Updates {
			h.dispatchUpdate(ctx, entities, update)
		}

	case *tg.UpdateShort:
		emptyEntities := tg.Entities{
			Users:    make(map[int64]*tg.User),
			Chats:    make(map[int64]*tg.Chat),
			Channels: make(map[int64]*tg.Channel),
		}
		h.dispatchUpdate(ctx, emptyEntities, updates.Update)
	}
	return nil
}

func (h *AccountMessageHandler) dispatchUpdate(ctx context.Context, entities tg.Entities, update tg.UpdateClass) {
	switch upd := update.(type) {
	case *tg.UpdateNewMessage:
		if msg, ok := upd.Message.(*tg.Message); ok {
			_ = h.OnNewMessage(ctx, entities, msg)
		}
	case *tg.UpdateNewChannelMessage:
		if msg, ok := upd.Message.(*tg.Message); ok {
			_ = h.OnNewMessage(ctx, entities, msg)
		}
	}
}

func buildEntities(users []tg.UserClass, chats []tg.ChatClass) tg.Entities {
	entities := tg.Entities{
		Users:    make(map[int64]*tg.User, len(users)),
		Chats:    make(map[int64]*tg.Chat),
		Channels: make(map[int64]*tg.Channel),
	}
	for _, userClass := range users {
		if user, ok := userClass.(*tg.User); ok {
			entities.Users[user.ID] = user
		}
	}
	for _, chatClass := range chats {
		if chat, ok := chatClass.(*tg.Chat); ok {
			entities.Chats[chat.ID] = chat
		} else if channel, ok := chatClass.(*tg.Channel); ok {
			entities.Channels[channel.ID] = channel
		}
	}
	return entities
}
