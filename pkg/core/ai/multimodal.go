package ai

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
)

const (
	// MaxImageAttachmentBytes caps each raw binary image payload sent to Vision models.
	MaxImageAttachmentBytes = 10 << 20
	// MaxImageAttachments caps the number of image parts attached to one prompt.
	MaxImageAttachments = 8
)

// ImageAttachment carries an in-memory image payload for multimodal Vision requests.
type ImageAttachment struct {
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
	Detail   string `json:"detail,omitempty"`
}

// NormalizeImageMIMEType validates supported raster formats and normalizes aliases.
func NormalizeImageMIMEType(mime string) (string, bool) {
	clean := strings.ToLower(strings.TrimSpace(mime))
	if idx := strings.IndexByte(clean, ';'); idx >= 0 {
		clean = strings.TrimSpace(clean[:idx])
	}
	switch clean {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return clean, true
	case "image/jpg":
		return "image/jpeg", true
	default:
		return "", false
	}
}

// ValidateImageAttachments checks count, MIME allowlist, and size bounds before serialization.
func ValidateImageAttachments(images []ImageAttachment) error {
	if len(images) > MaxImageAttachments {
		return &ClientError{
			Kind:    "invalid_image",
			Message: fmt.Sprintf("too many image attachments (%d > %d)", len(images), MaxImageAttachments),
		}
	}
	for i, img := range images {
		if _, ok := NormalizeImageMIMEType(img.MIMEType); !ok {
			return &ClientError{
				Kind:    "invalid_image",
				Message: fmt.Sprintf("image[%d] unsupported mime type %q", i, img.MIMEType),
			}
		}
		if len(img.Data) == 0 {
			return &ClientError{
				Kind:    "invalid_image",
				Message: fmt.Sprintf("image[%d] payload is empty", i),
			}
		}
		if len(img.Data) > MaxImageAttachmentBytes {
			return &ClientError{
				Kind:    "invalid_image",
				Message: fmt.Sprintf("image[%d] exceeds 10 MiB limit (%d bytes)", i, len(img.Data)),
			}
		}
	}
	return nil
}

func validateVisionRequest(profile config.AIProfile, images []ImageAttachment) error {
	if len(images) == 0 {
		return nil
	}
	if !profile.Capabilities.Vision {
		return &ClientError{
			Kind:    "vision_unsupported",
			Message: fmt.Sprintf("AI profile %q (%s) does not declare vision capability", profile.ID, profile.Model),
		}
	}
	return ValidateImageAttachments(images)
}

// ResolveVisionProfile selects the best configured profile capable of multimodal Vision:
//  1. Explicit cfg.VisionProfileID when present and Vision-capable.
//  2. ActiveProfile when its Capabilities.Vision is true.
//  3. First Vision-capable entry in cfg.Profiles.
func ResolveVisionProfile(cfg config.AIConfig) (config.AIProfile, error) {
	if trimmed := strings.TrimSpace(cfg.VisionProfileID); trimmed != "" {
		if prof, ok := cfg.ProfileByID(trimmed); ok && prof.Capabilities.Vision {
			if prof.Protocol == "" {
				prof.Protocol = config.ProtocolOpenAICompatible
			}
			return prof, nil
		}
	}
	active := cfg.ActiveProfile()
	if active.Capabilities.Vision {
		return active, nil
	}
	for _, prof := range cfg.Profiles {
		if prof.Capabilities.Vision {
			if prof.Protocol == "" {
				prof.Protocol = config.ProtocolOpenAICompatible
			}
			return prof, nil
		}
	}
	return config.AIProfile{}, &ClientError{
		Kind:    "vision_unsupported",
		Message: "no AI profile with vision capability is configured",
	}
}

func encodeOpenAIUserContent(userPrompt string, images []ImageAttachment) any {
	if len(images) == 0 {
		return userPrompt
	}
	parts := make([]map[string]any, 0, len(images)+1)
	if strings.TrimSpace(userPrompt) != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": userPrompt,
		})
	}
	for _, img := range images {
		mime, _ := NormalizeImageMIMEType(img.MIMEType)
		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		imageURL := map[string]any{"url": dataURL}
		if detail := strings.TrimSpace(img.Detail); detail != "" {
			imageURL["detail"] = detail
		}
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": imageURL,
		})
	}
	return parts
}

func encodeAnthropicUserContent(userPrompt string, images []ImageAttachment) any {
	if len(images) == 0 {
		return userPrompt
	}
	parts := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		mime, _ := NormalizeImageMIMEType(img.MIMEType)
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mime,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	if strings.TrimSpace(userPrompt) != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": userPrompt,
		})
	}
	return parts
}

func encodeGeminiUserParts(userPrompt string, images []ImageAttachment) []map[string]any {
	parts := make([]map[string]any, 0, len(images)+1)
	if strings.TrimSpace(userPrompt) != "" {
		parts = append(parts, map[string]any{"text": userPrompt})
	}
	for _, img := range images {
		mime, _ := NormalizeImageMIMEType(img.MIMEType)
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": mime,
				"data":      base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	return parts
}
