// internal/delivery/telegram/message_handler.go
package telegram

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type AuctionHandler struct {
	UseCase   domain.AuctionUseCase
	AIUseCase domain.AIGatewayUseCase
	Logger    *zap.Logger
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
		if !ok {
			h.Logger.Error("User not found in entities", zap.Int64("user_id", p.UserID))
			return nil
		}
		senderName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
		peer = &tg.InputPeerUser{
			UserID:     user.ID,
			AccessHash: user.AccessHash,
		}
	case *tg.PeerChat:
		groupID = p.ChatID
		peer = &tg.InputPeerChat{
			ChatID: p.ChatID,
		}
	case *tg.PeerChannel:
		groupID = p.ChannelID
		channel, ok := entities.Channels[p.ChannelID]
		if !ok {
			h.Logger.Error("Channel not found in entities", zap.Int64("channel_id", p.ChannelID))
			return nil
		}
		peer = &tg.InputPeerChannel{
			ChannelID:  channel.ID,
			AccessHash: channel.AccessHash,
		}
	}

	if peer == nil {
		h.Logger.Error("Failed to resolve input peer")
		return nil
	}

	// Route to AI Gateway if it's a private message
	if isPrivate && h.AIUseCase != nil {
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

	h.Logger.Debug("Incoming group/channel message",
		zap.Int64("group_id", groupID),
		zap.Int("topic_id", topicID),
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
			delay := time.Duration(rand.Intn(3000)+2000) * time.Millisecond
			time.Sleep(delay)

			err := h.UseCase.ExecuteBid(context.Background(), p, mID, r)
			if err != nil {
				h.Logger.Error("Failed to execute bid", zap.Error(err))
			}
		}(rule, peer, msg.ID)

		return nil
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
	switch updates := u.(type) {
	case *tg.UpdateShortMessage:
		if updates.Out {
			return nil
		}
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
	}
	return nil
}
