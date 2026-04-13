// internal/delivery/telegram/message_handler.go
package telegram

import (
	"context"
	"math/rand"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type AuctionHandler struct {
	UseCase domain.AuctionUseCase
	Logger  *zap.Logger
}

func (h *AuctionHandler) OnNewMessage(ctx context.Context, entities tg.Entities, msg *tg.Message) error {
	if msg.Out {
		return nil
	}

	text := msg.Message

	// logic 1: Cek apakah ada barang baru yang dilelang
	rule, err := h.UseCase.CheckKeyword(text)
	if err == nil && rule != nil && !rule.HasBidded {
		h.Logger.Info("Keyword detected", zap.String("keyword", rule.Keyword), zap.String("text", text))

		delay := time.Duration(rand.Intn(3000)+2000) * time.Millisecond
		time.Sleep(delay)

		// Manual Extract InputPeer
		var peer tg.InputPeerClass
		switch p := msg.PeerID.(type) {
		case *tg.PeerUser:
			user, ok := entities.Users[p.UserID]
			if !ok {
				h.Logger.Error("User not found in entities", zap.Int64("user_id", p.UserID))
				return nil
			}
			peer = &tg.InputPeerUser{
				UserID:     user.ID,
				AccessHash: user.AccessHash,
			}
		case *tg.PeerChat:
			peer = &tg.InputPeerChat{
				ChatID: p.ChatID,
			}
		case *tg.PeerChannel:
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

		err = h.UseCase.ExecuteBid(ctx, peer, rule)
		if err != nil {
			h.Logger.Error("Failed to execute bid", zap.Error(err))
		}
		return err
	}
	return nil
}

// Implementasi UpdateHandler dari gotd
func (h *AuctionHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	switch updates := u.(type) {
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
	}
	return nil
}
