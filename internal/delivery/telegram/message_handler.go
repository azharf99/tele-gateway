// internal/delivery/telegram/message_handler.go
package telegram

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// seenMessageTTL is how long a (peer, message) pair is remembered for
// de-duplication. Comfortably longer than the 2–5s bid delay, short enough that
// the map stays small.
const seenMessageTTL = 5 * time.Minute

type AuctionHandler struct {
	UseCase   domain.AuctionUseCase
	AIUseCase domain.AIGatewayUseCase
	Logger    *zap.Logger

	// BidDelay returns how long to wait before sending a bid. Defaults to a
	// random 2–5s (human simulation, anti-ban); tests override it.
	BidDelay func() time.Duration

	seenMu   sync.Mutex
	seenMsgs map[string]time.Time
}

func (h *AuctionHandler) bidDelay() time.Duration {
	if h.BidDelay != nil {
		return h.BidDelay()
	}
	return time.Duration(rand.Intn(3000)+2000) * time.Millisecond
}

// alreadyHandled reports whether this exact (peer, message) pair has been
// processed recently, and records it otherwise.
//
// Telegram delivers the same message more than once — the production logs show
// a single auction post producing two "Keyword detected" lines 150ms apart for
// the same msg_id, because it arrived both inside *tg.Updates and again in
// another envelope. Without this guard that posts the bid twice, which is
// exactly the kind of behaviour that gets a userbot banned.
func (h *AuctionHandler) alreadyHandled(peerID int64, msgID int) bool {
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

func (h *AuctionHandler) OnNewMessage(ctx context.Context, entities tg.Entities, msg *tg.Message) error {
	if msg.Out {
		return nil
	}

	text := msg.Message
	topicID := extractTopicID(msg)

	// Manual Extract InputPeer
	var peer tg.InputPeerClass
	var groupID int64
	var isPrivate bool
	var senderName string

	switch p := msg.PeerID.(type) {
	case *tg.PeerUser:
		isPrivate = true
		groupID = p.UserID
		user, ok := entities.Users[p.UserID]
		if ok {
			senderName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
			peer = &tg.InputPeerUser{
				UserID:     user.ID,
				AccessHash: user.AccessHash,
			}
		} else {
			// Fallback jika user tidak ada di entities (biasanya pada UpdateShort)
			senderName = "User"
			peer = &tg.InputPeerUser{
				UserID:     p.UserID,
				AccessHash: 0, // Akan dicoba resolusi otomatis oleh library sender
			}
			h.Logger.Info("User info missing in entities, using fallback", zap.Int64("user_id", p.UserID))
		}
	case *tg.PeerChat:
		groupID = p.ChatID
		peer = &tg.InputPeerChat{
			ChatID: p.ChatID,
		}
	case *tg.PeerChannel:
		groupID = p.ChannelID

		// Only a full channel constructor carries an access hash that Telegram
		// accepts on outgoing calls. A "min" one is valid just for reading the
		// update it came with, and sending to it fails with CHANNEL_INVALID —
		// which is exactly how bids in forum groups were being lost. Leave the
		// hash at 0 in that case and let the client resolve a real one.
		var accessHash int64
		switch channel, ok := entities.Channels[p.ChannelID]; {
		case ok && !channel.Min:
			accessHash = channel.AccessHash
		case ok:
			h.Logger.Info("Channel entity is min, access hash will be resolved before sending",
				zap.Int64("channel_id", p.ChannelID))
		default:
			h.Logger.Info("Channel not found in entities, access hash will be resolved before sending",
				zap.Int64("channel_id", p.ChannelID))
		}

		peer = &tg.InputPeerChannel{
			ChannelID:  p.ChannelID,
			AccessHash: accessHash,
		}
	}

	if peer == nil {
		h.Logger.Error("Failed to resolve input peer")
		return nil
	}

	if h.alreadyHandled(groupID, msg.ID) {
		h.Logger.Info("Duplicate update ignored", zap.Int64("group_id", groupID), zap.Int("msg_id", msg.ID))
		return nil
	}

	// Route to AI Gateway if it's a private message
	if isPrivate && h.AIUseCase != nil {
		h.Logger.Info("Private message detected, routing to AI Gateway", zap.Int64("user_id", groupID), zap.String("sender", senderName))
		replyFunc := func(replyText string) error {
			return h.UseCase.ReplyToUser(context.Background(), peer, msg.ID, replyText)
		}
		// Run in background to avoid blocking update handler
		go func() {
			err := h.AIUseCase.HandlePrivateMessage(context.Background(), groupID, senderName, text, replyFunc)
			if err != nil {
				h.Logger.Error("Failed to handle private message in AI Gateway", zap.Error(err))
			}
		}()
		return nil
	}

	// DIAGNOSTIC: promoted to Info so the bidding path is visible under zap.NewProduction().
	// Compare group_id/topic_id here against the target_group_id/topic_id in your bid_rules row.
	h.Logger.Info("Incoming group/channel message",
		zap.Int64("group_id", groupID),
		zap.Int("topic_id", topicID),
		zap.Int("msg_id", msg.ID),
		zap.String("text", text),
	)

	// Existing Bidding Logic
	err := h.UseCase.CheckAndStopByText(ctx, text, groupID, topicID)
	if err != nil {
		h.Logger.Error("Failed to check stop keywords", zap.Error(err))
	}

	rule, err := h.UseCase.CheckKeyword(text, groupID, topicID)
	if err == nil && rule != nil && !rule.HasBidded {
		h.Logger.Info("Keyword detected, scheduling bid...", zap.String("keyword", rule.Keyword), zap.Int("topic_id", topicID), zap.Int("msg_id", msg.ID))

		go func(r *domain.BidRule, p tg.InputPeerClass, mID int) {
			time.Sleep(h.bidDelay())

			err := h.UseCase.ExecuteBid(context.Background(), p, mID, r)
			if err != nil {
				h.Logger.Error("Failed to execute bid", zap.Error(err))
			}
		}(rule, peer, msg.ID)

		return nil
	}

	// DIAGNOSTIC: explain why no bid was scheduled instead of silently returning.
	switch {
	case err != nil:
		h.Logger.Info("No matching bid rule for message",
			zap.Int64("group_id", groupID),
			zap.Int("topic_id", topicID),
			zap.Error(err))
	case rule != nil && rule.HasBidded:
		h.Logger.Info("Matching rule found but already bidded (has_bidded=true)",
			zap.Uint("rule_id", rule.ID),
			zap.String("keyword", rule.Keyword))
	default:
		h.Logger.Info("No bid scheduled (no active rule matched)",
			zap.Int64("group_id", groupID),
			zap.Int("topic_id", topicID))
	}
	return nil
}

func extractTopicID(msg *tg.Message) int {
	if msg == nil {
		return 0
	}

	if msg.ReplyTo != nil {
		if header, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
			if header.ReplyToTopID > 0 {
				return header.ReplyToTopID
			}
			if header.ForumTopic && header.ReplyToMsgID > 0 {
				return header.ReplyToMsgID
			}
		}
	}

	return 0
}

// Implementasi UpdateHandler dari gotd
func (h *AuctionHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	h.Logger.Info("Incoming UpdatesClass", zap.String("type", fmt.Sprintf("%T", u)))
	switch updates := u.(type) {
	case *tg.UpdateShortMessage:
		if updates.Out {
			return nil
		}
		if h.alreadyHandled(updates.UserID, updates.ID) {
			return nil
		}
		h.Logger.Info("Private message detected (short)", zap.Int64("user_id", updates.UserID))
		if h.AIUseCase != nil {
			peer := &tg.InputPeerUser{
				UserID: updates.UserID,
			}
			replyFunc := func(replyText string) error {
				return h.UseCase.ReplyToUser(context.Background(), peer, updates.ID, replyText)
			}
			go func() {
				err := h.AIUseCase.HandlePrivateMessage(context.Background(), updates.UserID, "User", updates.Message, replyFunc)
				if err != nil {
					h.Logger.Error("Failed to handle short private message in AI Gateway", zap.Error(err))
				}
			}()
		}
	case *tg.Updates:
		entities := tg.Entities{
			Users:    make(map[int64]*tg.User),
			Chats:    make(map[int64]*tg.Chat),
			Channels: make(map[int64]*tg.Channel),
		}

		for _, userClass := range updates.GetUsers() {
			if user, ok := userClass.(*tg.User); ok {
				entities.Users[user.ID] = user
			}
		}
		for _, chatClass := range updates.GetChats() {
			if chat, ok := chatClass.(*tg.Chat); ok {
				entities.Chats[chat.ID] = chat
			} else if channel, ok := chatClass.(*tg.Channel); ok {
				entities.Channels[channel.ID] = channel
			}
		}

		for _, update := range updates.Updates {
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
	case *tg.UpdatesCombined:
		entities := tg.Entities{
			Users:    make(map[int64]*tg.User),
			Chats:    make(map[int64]*tg.Chat),
			Channels: make(map[int64]*tg.Channel),
		}

		for _, userClass := range updates.GetUsers() {
			if user, ok := userClass.(*tg.User); ok {
				entities.Users[user.ID] = user
			}
		}
		for _, chatClass := range updates.GetChats() {
			if chat, ok := chatClass.(*tg.Chat); ok {
				entities.Chats[chat.ID] = chat
			} else if channel, ok := chatClass.(*tg.Channel); ok {
				entities.Channels[channel.ID] = channel
			}
		}

		for _, update := range updates.Updates {
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
	case *tg.UpdateShort:
		h.Logger.Info("Handling short update", zap.String("inner_type", fmt.Sprintf("%T", updates.Update)))
		switch upd := updates.Update.(type) {
		case *tg.UpdateNewMessage:
			if msg, ok := upd.Message.(*tg.Message); ok {
				// For UpdateShort, we don't have entities. We'll pass empty ones
				// and OnNewMessage should handle it gracefully or we should fetch them.
				_ = h.OnNewMessage(ctx, tg.Entities{
					Users:    make(map[int64]*tg.User),
					Chats:    make(map[int64]*tg.Chat),
					Channels: make(map[int64]*tg.Channel),
				}, msg)
			}
		case *tg.UpdateNewChannelMessage:
			if msg, ok := upd.Message.(*tg.Message); ok {
				_ = h.OnNewMessage(ctx, tg.Entities{
					Users:    make(map[int64]*tg.User),
					Chats:    make(map[int64]*tg.Chat),
					Channels: make(map[int64]*tg.Channel),
				}, msg)
			}
		}
	default:
		h.Logger.Warn("Unhandled update type", zap.String("type", fmt.Sprintf("%T", u)))
	}
	return nil
}
