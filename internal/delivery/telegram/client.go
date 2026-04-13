// internal/delivery/telegram/client.go
package telegram

import (
	"context"
	"fmt"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
)

type TelegramClient struct {
	Client *telegram.Client
	Sender *message.Sender
}

func NewTelegramClient(appID int, appHash string, sessionPath string, handler telegram.UpdateHandler) (*TelegramClient, error) {
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: &telegram.FileSessionStorage{
			Path: sessionPath,
		},
		UpdateHandler: handler,
	})

	return &TelegramClient{
		Client: client,
		Sender: message.NewSender(tg.NewClient(client)),
	}, nil
}

// Implementasi interface TelegramService yang ada di usecase
func (c *TelegramClient) Reply(ctx context.Context, peer tg.InputPeerClass, message string) error {
	_, err := c.Sender.To(peer).Text(ctx, message)
	if err != nil {
		return fmt.Errorf("failed to send reply: %w", err)
	}
	return nil
}

func (c *TelegramClient) GetGroups(ctx context.Context) ([]domain.GroupInfo, error) {
	raw := tg.NewClient(c.Client)
	dialogs, err := raw.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit: 100,
	})
	if err != nil {
		return nil, fmt.Errorf("telegram rpc error: %w", err)
	}

	var groups []domain.GroupInfo
	var chats []tg.ChatClass

	switch v := dialogs.(type) {
	case *tg.MessagesDialogs:
		chats = v.Chats
	case *tg.MessagesDialogsSlice:
		chats = v.Chats
	case *tg.MessagesDialogsNotModified:
		return nil, fmt.Errorf("no changes in dialogs")
	default:
		return nil, fmt.Errorf("unexpected telegram response type: %T", v)
	}

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
