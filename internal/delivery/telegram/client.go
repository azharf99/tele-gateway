// internal/delivery/telegram/client.go
package telegram

import (
	"context"
	"fmt"
	"strings"

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
func (c *TelegramClient) Reply(ctx context.Context, peer tg.InputPeerClass, msgID int, message string) error {
	builder := c.Sender.To(peer).CloneBuilder()
	if msgID > 0 {
		builder = builder.Reply(msgID)
	}
	_, err := builder.Text(ctx, message)
	if err != nil {
		return fmt.Errorf("failed to send reply: %w", err)
	}
	return nil
}


func (c *TelegramClient) GetGroups(ctx context.Context) ([]domain.GroupInfo, error) {
	raw := tg.NewClient(c.Client)
	dialogs, err := raw.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
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

func (c *TelegramClient) GetTopics(ctx context.Context, groupID int64) ([]domain.TopicInfo, error) {
	raw := tg.NewClient(c.Client)
	peer, err := c.resolveInputPeerChannel(ctx, raw, groupID)
	if err != nil {
		if strings.Contains(err.Error(), "not a supergroup") {
			return []domain.TopicInfo{{ID: 0, Title: "General"}}, nil
		}
		return nil, err
	}

	resp, err := raw.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
		Peer:        peer,
		OffsetDate:  0,
		OffsetID:    0,
		OffsetTopic: 0,
		Limit:       100,
	})
	if err != nil {
		if strings.Contains(err.Error(), "CHANNEL_FORUM_MISSING") || strings.Contains(err.Error(), "PEER_ID_INVALID") {
			return []domain.TopicInfo{{ID: 0, Title: "General"}}, nil
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

func (c *TelegramClient) resolveInputPeerChannel(ctx context.Context, raw *tg.Client, groupID int64) (*tg.InputPeerChannel, error) {
	dialogs, err := raw.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err != nil {
		return nil, fmt.Errorf("telegram rpc error: %w", err)
	}

	var chats []tg.ChatClass
	switch v := dialogs.(type) {
	case *tg.MessagesDialogs:
		chats = v.Chats
	case *tg.MessagesDialogsSlice:
		chats = v.Chats
	default:
		return nil, fmt.Errorf("unexpected telegram response type: %T", v)
	}

	for _, chatClass := range chats {
		channel, ok := chatClass.(*tg.Channel)
		if !ok {
			continue
		}
		if channel.ID != groupID {
			continue
		}
		if !channel.Megagroup {
			return nil, fmt.Errorf("group %d is not a supergroup", groupID)
		}
		return &tg.InputPeerChannel{
			ChannelID:  channel.ID,
			AccessHash: channel.AccessHash,
		}, nil
	}

	return nil, fmt.Errorf("supergroup with id %d not found in dialogs", groupID)
}
