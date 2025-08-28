package chatwoot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

// sanitizeMimeType remove informações de codec do MIME type (ex: "audio/ogg; codecs=opus" -> "audio/ogg")
func sanitizeMimeType(mimeType string) string {
	if mimeType == "" {
		return mimeType
	}
	
	// Separar pelo ponto e vírgula e pegar apenas a primeira parte
	parts := strings.Split(mimeType, ";")
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}
	
	return mimeType
}

// downloadWhatsAppMedia faz download de mídia do WhatsApp
func downloadWhatsAppMedia(client *whatsmeow.Client, evt *events.Message) (*MediaData, error) {
	if client == nil {
		return nil, fmt.Errorf("WhatsApp client is nil")
	}

	var mediaData *MediaData
	var err error

	// Processar diferentes tipos de mídia
	if evt.Message.ImageMessage != nil {
		mediaData, err = downloadImageMessage(client, evt.Message.ImageMessage)
	} else if evt.Message.VideoMessage != nil {
		mediaData, err = downloadVideoMessage(client, evt.Message.VideoMessage)
	} else if evt.Message.AudioMessage != nil {
		mediaData, err = downloadAudioMessage(client, evt.Message.AudioMessage)
	} else if evt.Message.DocumentMessage != nil {
		mediaData, err = downloadDocumentMessage(client, evt.Message.DocumentMessage)
	} else if evt.Message.StickerMessage != nil {
		mediaData, err = downloadStickerMessage(client, evt.Message.StickerMessage)
	} else {
		return nil, fmt.Errorf("unsupported media type")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to download media: %w", err)
	}

	log.Info().
		Str("messageID", evt.Info.ID).
		Str("mediaType", string(mediaData.MessageType)).
		Str("mimeType", mediaData.MimeType).
		Str("fileName", mediaData.FileName).
		Int64("fileSize", mediaData.FileSize).
		Msg("Successfully downloaded WhatsApp media")

	return mediaData, nil
}

// downloadImageMessage baixa mensagem de imagem
func downloadImageMessage(client *whatsmeow.Client, msg *waE2E.ImageMessage) (*MediaData, error) {
	data, err := client.Download(context.Background(), msg)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}

	mimeType := "image/jpeg" // default
	if msg.Mimetype != nil {
		mimeType = sanitizeMimeType(*msg.Mimetype)
	}

	var caption string
	if msg.Caption != nil {
		caption = *msg.Caption
	}

	var fileName string
	// ImageMessage não tem FileName, gerar nome baseado no tipo
	fileName = GenerateFileName("", mimeType)

	return &MediaData{
		Data:        data,
		MimeType:    mimeType,
		FileName:    fileName,
		Caption:     caption,
		FileSize:    int64(len(data)),
		MessageType: MediaTypeImage,
	}, nil
}

// downloadVideoMessage baixa mensagem de vídeo
func downloadVideoMessage(client *whatsmeow.Client, msg *waE2E.VideoMessage) (*MediaData, error) {
	data, err := client.Download(context.Background(), msg)
	if err != nil {
		return nil, fmt.Errorf("failed to download video: %w", err)
	}

	mimeType := "video/mp4" // default
	if msg.Mimetype != nil {
		mimeType = sanitizeMimeType(*msg.Mimetype)
	}

	var caption string
	if msg.Caption != nil {
		caption = *msg.Caption
	}

	var fileName string
	// VideoMessage não tem FileName, gerar nome baseado no tipo
	fileName = GenerateFileName("", mimeType)

	return &MediaData{
		Data:        data,
		MimeType:    mimeType,
		FileName:    fileName,
		Caption:     caption,
		FileSize:    int64(len(data)),
		MessageType: MediaTypeVideo,
	}, nil
}

// downloadAudioMessage baixa mensagem de áudio
func downloadAudioMessage(client *whatsmeow.Client, msg *waE2E.AudioMessage) (*MediaData, error) {
	data, err := client.Download(context.Background(), msg)
	if err != nil {
		return nil, fmt.Errorf("failed to download audio: %w", err)
	}

	mimeType := "audio/mpeg" // default
	if msg.Mimetype != nil {
		mimeType = sanitizeMimeType(*msg.Mimetype)
	}

	var fileName string
	// AudioMessage não tem FileName, gerar nome baseado no tipo
	fileName = GenerateFileName("", mimeType)

	return &MediaData{
		Data:        data,
		MimeType:    mimeType,
		FileName:    fileName,
		FileSize:    int64(len(data)),
		MessageType: MediaTypeAudio,
	}, nil
}

// downloadDocumentMessage baixa mensagem de documento
func downloadDocumentMessage(client *whatsmeow.Client, msg *waE2E.DocumentMessage) (*MediaData, error) {
	data, err := client.Download(context.Background(), msg)
	if err != nil {
		return nil, fmt.Errorf("failed to download document: %w", err)
	}

	mimeType := "application/octet-stream" // default
	if msg.Mimetype != nil {
		mimeType = sanitizeMimeType(*msg.Mimetype)
	}

	var caption string
	if msg.Caption != nil {
		caption = *msg.Caption
	}

	var fileName string
	if msg.FileName != nil && *msg.FileName != "" {
		fileName = *msg.FileName
	} else {
		fileName = GenerateFileName("document", mimeType)
	}

	return &MediaData{
		Data:        data,
		MimeType:    mimeType,
		FileName:    fileName,
		Caption:     caption,
		FileSize:    int64(len(data)),
		MessageType: MediaTypeDocument,
	}, nil
}

// CreateMediaReader cria um io.Reader a partir dos dados de mídia
func CreateMediaReader(mediaData *MediaData) io.Reader {
	return bytes.NewReader(mediaData.Data)
}

// ValidateMediaData valida os dados de mídia
func ValidateMediaData(mediaData *MediaData) error {
	if mediaData == nil {
		return fmt.Errorf("media data is nil")
	}

	if len(mediaData.Data) == 0 {
		return fmt.Errorf("media data is empty")
	}

	if mediaData.MimeType == "" {
		return fmt.Errorf("mime type is required")
	}

	if mediaData.FileName == "" {
		return fmt.Errorf("file name is required")
	}

	if !IsValidMediaType(mediaData.MimeType) {
		return fmt.Errorf("unsupported media type: %s", mediaData.MimeType)
	}

	// Validar tamanho máximo (50MB)
	maxSize := int64(50 * 1024 * 1024) // 50MB
	if mediaData.FileSize > maxSize {
		return fmt.Errorf("file size too large: %d bytes (max: %d bytes)", 
			mediaData.FileSize, maxSize)
	}

	log.Debug().
		Str("mimeType", mediaData.MimeType).
		Str("fileName", mediaData.FileName).
		Int64("fileSize", mediaData.FileSize).
		Str("mediaType", string(mediaData.MessageType)).
		Msg("Media data validation passed")

	return nil
}

// downloadStickerMessage baixa mensagem de sticker
func downloadStickerMessage(client *whatsmeow.Client, msg *waE2E.StickerMessage) (*MediaData, error) {
	data, err := client.Download(context.Background(), msg)
	if err != nil {
		return nil, fmt.Errorf("failed to download sticker: %w", err)
	}

	mimeType := "image/webp" // default para stickers
	if msg.Mimetype != nil {
		mimeType = sanitizeMimeType(*msg.Mimetype)
	}

	var fileName string
	// StickerMessage não tem FileName, gerar nome baseado no tipo
	fileName = GenerateFileName("sticker", mimeType)

	return &MediaData{
		Data:        data,
		MimeType:    mimeType,
		FileName:    fileName,
		Caption:     "", // Sem caption - apenas mídia pura
		FileSize:    int64(len(data)),
		MessageType: MediaTypeSticker,
	}, nil
}

// GetMediaInfo extrai informações da mídia para logging
func GetMediaInfo(mediaData *MediaData) MediaInfo {
	return MediaInfo{
		ContentType: mediaData.MimeType,
		FileName:    mediaData.FileName,
		FileSize:    mediaData.FileSize,
		MediaType:   string(mediaData.MessageType),
	}
}