package campaign

import (
	"context"
	"fmt"

	domainSend "github.com/aldinokemal/go-whatsapp-web-multidevice/domains/send"
	"github.com/aldinokemal/go-whatsapp-web-multidevice/infrastructure/whatsapp"
)

// MessageSender is the minimal delivery contract the campaign runner depends on.
// Keeping it small (rather than depending on the whole send usecase) makes the
// runner trivially testable with a fake.
type MessageSender interface {
	// SendText delivers a plain text message from deviceID to phone.
	SendText(ctx context.Context, deviceID, phone, message string) (messageID string, err error)
	// SendImageURL delivers an image (by URL) with a caption.
	SendImageURL(ctx context.Context, deviceID, phone, caption, imageURL string) (messageID string, err error)
	// SendComposing shows the "typing…" indicator to phone.
	SendComposing(ctx context.Context, deviceID, phone string) error
	// StopComposing clears the "typing…" indicator.
	StopComposing(ctx context.Context, deviceID, phone string) error
}

// WhatsAppSender adapts the device manager + send usecase into a MessageSender.
// It resolves the sending device per call and scopes the context to it, mirroring
// how the REST handlers send through a specific device.
type WhatsAppSender struct {
	dm  *whatsapp.DeviceManager
	snd domainSend.ISendUsecase
}

// NewWhatsAppSender builds the production MessageSender.
func NewWhatsAppSender(dm *whatsapp.DeviceManager, snd domainSend.ISendUsecase) *WhatsAppSender {
	return &WhatsAppSender{dm: dm, snd: snd}
}

// deviceCtx resolves deviceID and returns a context scoped to that device so the
// send usecase picks the right whatsmeow client.
func (s *WhatsAppSender) deviceCtx(ctx context.Context, deviceID string) (context.Context, error) {
	if s.dm == nil {
		return nil, fmt.Errorf("device manager unavailable")
	}
	inst, _, err := s.dm.ResolveDevice(deviceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, fmt.Errorf("device %q not found", deviceID)
	}
	return whatsapp.ContextWithDevice(ctx, inst), nil
}

func (s *WhatsAppSender) SendText(ctx context.Context, deviceID, phone, message string) (string, error) {
	dctx, err := s.deviceCtx(ctx, deviceID)
	if err != nil {
		return "", err
	}
	resp, err := s.snd.SendText(dctx, domainSend.MessageRequest{
		BaseRequest: domainSend.BaseRequest{Phone: phone},
		Message:     message,
	})
	return resp.MessageID, err
}

func (s *WhatsAppSender) SendImageURL(ctx context.Context, deviceID, phone, caption, imageURL string) (string, error) {
	dctx, err := s.deviceCtx(ctx, deviceID)
	if err != nil {
		return "", err
	}
	url := imageURL
	resp, err := s.snd.SendImage(dctx, domainSend.ImageRequest{
		BaseRequest: domainSend.BaseRequest{Phone: phone},
		Caption:     caption,
		ImageURL:    &url,
	})
	return resp.MessageID, err
}

func (s *WhatsAppSender) SendComposing(ctx context.Context, deviceID, phone string) error {
	return s.chatPresence(ctx, deviceID, phone, "start")
}

func (s *WhatsAppSender) StopComposing(ctx context.Context, deviceID, phone string) error {
	return s.chatPresence(ctx, deviceID, phone, "stop")
}

func (s *WhatsAppSender) chatPresence(ctx context.Context, deviceID, phone, action string) error {
	dctx, err := s.deviceCtx(ctx, deviceID)
	if err != nil {
		return err
	}
	_, err = s.snd.SendChatPresence(dctx, domainSend.ChatPresenceRequest{Phone: phone, Action: action})
	return err
}
