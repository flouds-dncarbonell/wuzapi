package chatwoot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	MergeBrazilContacts bool      `db:"merge_brazil_contacts" json:"merge_brazil_contacts"`
	IgnoreJids          string    `db:"ignore_jids" json:"ignore_jids"`
	CreatedAt           time.Time `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time `db:"updated_at" json:"updated_at"`
}

// Contact representa um contato no Chatwoot
type Contact struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// Conversation representa uma conversa no Chatwoot
type Conversation struct {
	ID        int    `json:"id"`
	ContactID int    `json:"contact_id"`
	InboxID   int    `json:"inbox_id"`
	Status    string `json:"status"`
}

// Message representa uma mensagem no Chatwoot
type Message struct {
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
	if config.Token == "" {
		return fmt.Errorf("token is required")
	}
	if config.URL == "" {
		return fmt.Errorf("url is required")
	}
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
	          merge_brazil_contacts, ignore_jids, created_at, updated_at 
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
		// Atualizar configuração existente
		query := `UPDATE chatwoot_configs SET 
		          enabled = $1, account_id = $2, token = $3, url = $4, 
		          name_inbox = $5, sign_msg = $6, sign_delimiter = $7, 
		          reopen_conversation = $8, conversation_pending = $9, 
		          merge_brazil_contacts = $10, ignore_jids = $11, updated_at = $12 
		          WHERE user_id = $13`
		
		result, err := db.Exec(query, config.Enabled, config.AccountID, config.Token, 
			config.URL, config.NameInbox, config.SignMsg, config.SignDelimiter,
			config.ReopenConversation, config.ConversationPending, config.MergeBrazilContacts,
			config.IgnoreJids, config.UpdatedAt, config.UserID)
		
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
		           merge_brazil_contacts, ignore_jids, created_at, updated_at) 
		          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
		
		result, err := db.Exec(query, config.ID, config.UserID, config.Enabled, config.AccountID, 
			config.Token, config.URL, config.NameInbox, config.SignMsg, config.SignDelimiter,
			config.ReopenConversation, config.ConversationPending, config.MergeBrazilContacts,
			config.IgnoreJids, config.CreatedAt, config.UpdatedAt)
		
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