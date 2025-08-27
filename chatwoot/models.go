package chatwoot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

// Config representa a configuração do usuário para integração com Chatwoot
type Config struct {
	ID                   string    `db:"id" json:"id"`
	UserID              string    `db:"user_id" json:"user_id"`
	Enabled             bool      `db:"enabled" json:"enabled"`
	AccountID           string    `db:"account_id" json:"account_id"`
	Token               string    `db:"token" json:"token"`
	URL                 string    `db:"url" json:"url"`
	NameInbox           string    `db:"name_inbox" json:"name_inbox"`
	SignMsg             bool      `db:"sign_msg" json:"sign_msg"`
	SignDelimiter       string    `db:"sign_delimiter" json:"sign_delimiter"`
	ReopenConversation  bool      `db:"reopen_conversation" json:"reopen_conversation"`
	ConversationPending bool      `db:"conversation_pending" json:"conversation_pending"`
	MergeBrazilContacts       bool      `db:"merge_brazil_contacts" json:"merge_brazil_contacts"`
	IgnoreJids   string `db:"ignore_jids" json:"ignore_jids"`
	IgnoreGroups bool   `db:"ignore_groups" json:"ignore_groups"`
	EnableTypingIndicator    bool      `db:"enable_typing_indicator" json:"enable_typing_indicator"`
	CreatedAt                time.Time `db:"created_at" json:"created_at"` 
	UpdatedAt                time.Time `db:"updated_at" json:"updated_at"`
}

// Contact representa um contato no Chatwoot
type Contact struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// ReplyInfo contém informações de uma mensagem sendo citada
type ReplyInfo struct {
	InReplyToExternalID  string `json:"in_reply_to_external_id"` // StanzaID do WhatsApp
	InReplyToChatwootID  int    `json:"in_reply_to"`             // ID da mensagem no Chatwoot
}

// Conversation representa uma conversa no Chatwoot
type Conversation struct {
	ID           int           `json:"id"`
	ContactID    int           `json:"contact_id"`
	InboxID      int           `json:"inbox_id"`
	Status       string        `json:"status"`
	ContactInbox *ContactInbox `json:"contact_inbox,omitempty"`
}

// ContactInbox representa a relação contato-inbox com source_id
type ContactInbox struct {
	ID        int    `json:"id"`
	ContactID int    `json:"contact_id"`
	InboxID   int    `json:"inbox_id"`
	SourceID  string `json:"source_id"`
}

// Message representa uma mensagem no Chatwoot (para envio)
type Message struct {
	ID             int    `json:"id"`
	Content        string `json:"content"`
	MessageType    string `json:"message_type"` // "incoming" ou "outgoing"
	ConversationID int    `json:"conversation_id"`
	SenderID       int    `json:"sender_id,omitempty"`
	SourceID       string `json:"source_id,omitempty"`
}

// MessageResponse representa a resposta da API do Chatwoot (recebe message_type como int)
type MessageResponse struct {
	ID             int    `json:"id"`
	Content        string `json:"content"`
	MessageType    int    `json:"message_type"` // 0=incoming, 1=outgoing
	ConversationID int    `json:"conversation_id"`
	SenderID       int    `json:"sender_id,omitempty"`
	SourceID       string `json:"source_id,omitempty"`
}

// Inbox representa um inbox no Chatwoot
type Inbox struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ValidateConfig valida os dados de configuração do Chatwoot
func ValidateConfig(config Config) error {
	if config.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if config.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if config.URL == "" {
		return fmt.Errorf("url is required")
	}
	// Token só é obrigatório se for uma nova configuração
	// Para atualizações, o token vazio será preservado no SaveConfig
	return nil
}

// GenerateID gera um ID único para configuração Chatwoot
func GenerateID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// GetConfigByUserID busca a configuração Chatwoot de um usuário
func GetConfigByUserID(db *sqlx.DB, userID string) (*Config, error) {
	log.Info().Str("userID", userID).Msg("=== STARTING GetConfigByUserID ===")
	
	var config Config
	query := `SELECT id, user_id, enabled, account_id, token, url, name_inbox, 
	          sign_msg, sign_delimiter, reopen_conversation, conversation_pending, 
	          merge_brazil_contacts, ignore_jids, ignore_groups, enable_typing_indicator,
	          created_at, updated_at 
	          FROM chatwoot_configs WHERE user_id = $1`
	
	log.Info().Str("userID", userID).Str("query", query).Msg("Executing database query")
	
	err := db.Get(&config, query, userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Database query failed")
		return nil, err
	}
	
	log.Info().Str("userID", userID).Str("configID", config.ID).Str("url", config.URL).Msg("Config found successfully")
	return &config, nil
}

// SaveConfig salva ou atualiza uma configuração Chatwoot
func SaveConfig(db *sqlx.DB, config Config) error {
	log.Info().
		Str("userID", config.UserID).
		Str("url", config.URL).
		Str("accountID", config.AccountID).
		Bool("enabled", config.Enabled).
		Msg("Starting SaveConfig")
	
	// Verificar se já existe uma configuração para este usuário
	existingConfig, err := GetConfigByUserID(db, config.UserID)
	if err != nil && err.Error() != "sql: no rows in result set" {
		log.Error().Err(err).Str("userID", config.UserID).Msg("Error checking existing config")
		return fmt.Errorf("error checking existing config: %w", err)
	}

	config.UpdatedAt = time.Now()

	if existingConfig != nil {
		log.Info().Str("userID", config.UserID).Msg("Updating existing config")
		// Preservar token existente se o novo estiver vazio
		tokenToSave := config.Token
		if config.Token == "" {
			tokenToSave = existingConfig.Token
			log.Info().Str("userID", config.UserID).Msg("Preserving existing token (new token was empty)")
		} else {
			log.Info().Str("userID", config.UserID).Msg("Using new token from request")
		}
		
		// Atualizar configuração existente
		query := `UPDATE chatwoot_configs SET 
		          enabled = $1, account_id = $2, token = $3, url = $4, 
		          name_inbox = $5, sign_msg = $6, sign_delimiter = $7, 
		          reopen_conversation = $8, conversation_pending = $9, 
		          merge_brazil_contacts = $10, ignore_jids = $11, ignore_groups = $12,
		          enable_typing_indicator = $13, updated_at = $14 
		          WHERE user_id = $15`
		
		result, err := db.Exec(query, config.Enabled, config.AccountID, tokenToSave, 
			config.URL, config.NameInbox, config.SignMsg, config.SignDelimiter,
			config.ReopenConversation, config.ConversationPending, config.MergeBrazilContacts,
			config.IgnoreJids, config.IgnoreGroups, config.EnableTypingIndicator, config.UpdatedAt, config.UserID)
		
		// Update config with preserved token for validation if needed
		config.Token = tokenToSave
		
		if err != nil {
			log.Error().Err(err).Str("userID", config.UserID).Msg("Error updating config")
		} else {
			rowsAffected, _ := result.RowsAffected()
			log.Info().Str("userID", config.UserID).Int64("rowsAffected", rowsAffected).Msg("Config updated")
		}
	} else {
		log.Info().Str("userID", config.UserID).Msg("Creating new config")
		// Criar nova configuração
		if config.ID == "" {
			config.ID = GenerateID()
		}
		config.CreatedAt = time.Now()
		
		log.Info().Str("userID", config.UserID).Str("configID", config.ID).Msg("Generated new config ID")

		query := `INSERT INTO chatwoot_configs 
		          (id, user_id, enabled, account_id, token, url, name_inbox, 
		           sign_msg, sign_delimiter, reopen_conversation, conversation_pending, 
		           merge_brazil_contacts, ignore_jids, ignore_groups, enable_typing_indicator,
		           created_at, updated_at) 
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`
		
		result, err := db.Exec(query, config.ID, config.UserID, config.Enabled, config.AccountID, 
			config.Token, config.URL, config.NameInbox, config.SignMsg, config.SignDelimiter,
			config.ReopenConversation, config.ConversationPending, config.MergeBrazilContacts,
			config.IgnoreJids, config.IgnoreGroups, config.EnableTypingIndicator, config.CreatedAt, config.UpdatedAt)
		
		if err != nil {
			log.Error().Err(err).Str("userID", config.UserID).Msg("Error inserting config")
		} else {
			rowsAffected, _ := result.RowsAffected()
			log.Info().Str("userID", config.UserID).Int64("rowsAffected", rowsAffected).Msg("Config inserted")
		}
	}

	if err != nil {
		log.Error().Err(err).Str("userID", config.UserID).Msg("SaveConfig failed")
	} else {
		log.Info().Str("userID", config.UserID).Msg("SaveConfig completed successfully")
	}

	return err
}

// DeleteConfig remove a configuração Chatwoot de um usuário
func DeleteConfig(db *sqlx.DB, userID string) error {
	query := `DELETE FROM chatwoot_configs WHERE user_id = $1`
	_, err := db.Exec(query, userID)
	return err
}

// ParseIgnoreJids converte a string JSON em slice de JIDs para ignorar
func ParseIgnoreJids(ignoreJidsJSON string) []string {
	var jids []string
	if ignoreJidsJSON == "" {
		return jids
	}
	
	err := json.Unmarshal([]byte(ignoreJidsJSON), &jids)
	if err != nil {
		return []string{} // Retorna slice vazio em caso de erro
	}
	return jids
}

// isGroupID verifica se o ID representa um grupo WhatsApp
func isGroupID(id string) bool {
	// Grupos WhatsApp têm IDs longos e não são números de telefone tradicionais
	// Geralmente são strings longas de números (mais de 15 caracteres)
	if len(id) > 15 {
		// Verificar se é só números (IDs de grupo são puramente numéricos)
		for _, char := range id {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// GetConfigByToken busca a configuração do Chatwoot pelo token do usuário
func GetConfigByToken(db *sqlx.DB, token string) (*Config, error) {
	log.Info().Str("token", token[:min(8, len(token))]+"...").Msg("=== STARTING GetConfigByToken ===")
	
	var config Config
	query := `SELECT c.id, c.user_id, c.enabled, c.account_id, c.token, c.url, c.name_inbox, 
	          c.sign_msg, c.sign_delimiter, c.reopen_conversation, c.conversation_pending, 
	          c.merge_brazil_contacts, c.ignore_jids, c.ignore_groups, c.enable_typing_indicator,
	          c.created_at, c.updated_at 
	          FROM chatwoot_configs c 
	          INNER JOIN users u ON u.id = c.user_id 
	          WHERE u.token = $1 AND c.enabled = true`
	
	log.Info().Str("token", token[:min(8, len(token))]+"...").Str("query", query).Msg("Executing database query")
	
	err := db.Get(&config, query, token)
	if err != nil {
		log.Error().Err(err).Str("token", token[:min(8, len(token))]+"...").Msg("Database query failed")
		return nil, fmt.Errorf("configuração não encontrada para token: %w", err)
	}
	
	log.Info().Str("userID", config.UserID).Str("configID", config.ID).Msg("Config found successfully by token")
	return &config, nil
}

// min função auxiliar para obter o menor valor entre dois inteiros
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MediaData representa dados de mídia para processamento
type MediaData struct {
	Data        []byte    `json:"data"`
	MimeType    string    `json:"mime_type"`
	FileName    string    `json:"file_name"`
	Caption     string    `json:"caption,omitempty"`
	FileSize    int64     `json:"file_size"`
	MessageType MediaType `json:"message_type"`
}

// MediaType representa os tipos de mídia suportados
type MediaType string

const (
	MediaTypeImage    MediaType = "image"
	MediaTypeVideo    MediaType = "video"
	MediaTypeAudio    MediaType = "audio"
	MediaTypeDocument MediaType = "document"
)

// MediaInfo contém informações da mídia para Chatwoot
type MediaInfo struct {
	ContentType string `json:"content_type"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	MediaType   string `json:"media_type"`
}

// GetMediaType detecta o tipo de mídia baseado no MIME type
func GetMediaType(mimeType string) MediaType {
	parts := strings.Split(mimeType, "/")
	if len(parts) < 1 {
		return MediaTypeDocument
	}
	
	switch parts[0] {
	case "image":
		return MediaTypeImage
	case "video":
		return MediaTypeVideo
	case "audio":
		return MediaTypeAudio
	default:
		return MediaTypeDocument
	}
}

// GetFileExtension obtém extensão do arquivo baseada no MIME type
// Prioriza extensões compatíveis com Chatwoot
func GetFileExtension(mimeType string) string {
	// Mapeamento direto para garantir compatibilidade com Chatwoot
	switch mimeType {
	// Imagens - usar extensões mais compatíveis
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	// Vídeos - usar extensões mais compatíveis
	case "video/mp4":
		return ".mp4"
	case "video/avi", "video/x-msvideo":
		return ".avi"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	// Áudios - usar extensões mais compatíveis
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/mp4":
		return ".m4a"
	// Documentos
	case "application/pdf":
		return ".pdf"
	case "application/msword":
		return ".doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	default:
		// Fallback: tentar extensões do sistema, mas filtrar as compatíveis
		extensions, err := mime.ExtensionsByType(mimeType)
		if err != nil || len(extensions) == 0 {
			return ".bin"
		}
		
		// Filtrar para extensões mais comuns/compatíveis
		for _, ext := range extensions {
			switch ext {
			case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
				return ext
			case ".mp4", ".avi", ".mov", ".webm":
				return ext
			case ".mp3", ".ogg", ".wav", ".m4a":
				return ext
			case ".pdf", ".doc", ".docx", ".txt", ".csv":
				return ext
			}
		}
		
		// Se não encontrou nenhuma compatível, usar a primeira
		return extensions[0]
	}
}

// GenerateFileName gera nome único para arquivo
func GenerateFileName(originalName, mimeType string) string {
	if originalName != "" {
		// Usar nome original se disponível
		ext := filepath.Ext(originalName)
		if ext == "" {
			ext = GetFileExtension(mimeType)
		}
		name := strings.TrimSuffix(originalName, ext)
		return fmt.Sprintf("%s_%d%s", name, time.Now().Unix(), ext)
	}
	
	// Gerar nome baseado no timestamp e tipo
	mediaType := GetMediaType(mimeType)
	ext := GetFileExtension(mimeType)
	return fmt.Sprintf("%s_%d%s", string(mediaType), time.Now().Unix(), ext)
}

// IsValidMediaType verifica se o tipo de mídia é suportado
func IsValidMediaType(mimeType string) bool {
	supportedTypes := map[string]bool{
		// Imagens
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
		"image/bmp":  true,
		// Vídeos
		"video/mp4":       true,
		"video/avi":       true,
		"video/quicktime": true,
		"video/x-msvideo": true,
		"video/webm":      true,
		// Áudios
		"audio/mpeg":     true,
		"audio/mp3":      true,
		"audio/ogg":      true,
		"audio/wav":      true,
		"audio/x-wav":    true,
		"audio/webm":     true,
		"audio/mp4":      true,
		// Documentos
		"application/pdf":                                                          true,
		"application/msword":                                                       true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		"application/vnd.ms-excel":                                                 true,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       true,
		"text/plain":                                                               true,
		"text/csv":                                                                 true,
	}
	
	return supportedTypes[mimeType]
}

// DeleteMessage deleta uma mensagem do banco local
func DeleteMessage(db *sqlx.DB, messageID, userID string) error {
	query := `DELETE FROM messages WHERE id = $1 AND user_id = $2`
	
	result, err := db.Exec(query, messageID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete message from database: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("message not found or not deleted")
	}

	return nil
}