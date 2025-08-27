package chatwoot

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

// MessageRecord representa uma mensagem salva no banco
type MessageRecord struct {
	ID                      string    `db:"id" json:"id"`                                             // WhatsApp message ID
	UserID                  string    `db:"user_id" json:"user_id"`                                   // Usuário dono da mensagem
	Content                 string    `db:"content" json:"content"`                                   // Conteúdo da mensagem
	SenderName              string    `db:"sender_name" json:"sender_name"`                           // Nome do remetente
	MessageType             string    `db:"message_type" json:"message_type"`                         // "text", "image", "video", etc.
	ChatwootMessageID       *int      `db:"chatwoot_message_id" json:"chatwoot_message_id"`           // ID no Chatwoot (NULL até ser enviada)
	ChatwootConversationID  *int      `db:"chatwoot_conversation_id" json:"chatwoot_conversation_id"` // ID da conversa no Chatwoot
	FromMe                  bool      `db:"from_me" json:"from_me"`                                   // Se a mensagem é do próprio bot
	ChatJID                 string    `db:"chat_jid" json:"chat_jid"`                                 // JID do chat (individual ou grupo)
	CreatedAt               time.Time `db:"created_at" json:"created_at"`                             // Timestamp de criação
	UpdatedAt               time.Time `db:"updated_at" json:"updated_at"`                             // Timestamp da última atualização
}

// SaveMessage salva uma nova mensagem no banco
func SaveMessage(db *sqlx.DB, msg MessageRecord) error {
	log.Debug().
		Str("id", msg.ID).
		Str("user_id", msg.UserID).
		Str("content", truncateString(msg.Content, 50)).
		Str("message_type", msg.MessageType).
		Bool("from_me", msg.FromMe).
		Str("chat_jid", msg.ChatJID).
		Interface("chatwoot_message_id", msg.ChatwootMessageID).
		Msg("📤 SaveMessage called with MessageRecord")

	query := `
		INSERT INTO messages (id, user_id, content, sender_name, message_type, chatwoot_message_id, chatwoot_conversation_id, from_me, chat_jid, created_at, updated_at)
		VALUES (:id, :user_id, :content, :sender_name, :message_type, :chatwoot_message_id, :chatwoot_conversation_id, :from_me, :chat_jid, :created_at, :updated_at)
	`
	
	// Definir timestamp se não fornecido
	now := time.Now()
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = now
	}
	if msg.UpdatedAt.IsZero() {
		msg.UpdatedAt = now
	}
	
	_, err := db.NamedExec(query, msg)
	if err != nil {
		return fmt.Errorf("failed to save message: %w", err)
	}

	log.Debug().
		Str("message_id", msg.ID).
		Str("user_id", msg.UserID).
		Str("content", truncateString(msg.Content, 50)).
		Str("message_type", msg.MessageType).
		Bool("from_me", msg.FromMe).
		Interface("chatwoot_message_id", msg.ChatwootMessageID).
		Bool("has_chatwoot_id", msg.ChatwootMessageID != nil).
		Msg("💾 Message saved to database")

	return nil
}

// UpdateMessageChatwootID atualiza o chatwoot_message_id de uma mensagem existente
func UpdateMessageChatwootID(db *sqlx.DB, whatsappMessageID string, chatwootMessageID int, userID string) error {
	query := `
		UPDATE messages 
		SET chatwoot_message_id = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND user_id = $3
	`
	
	result, err := db.Exec(query, chatwootMessageID, whatsappMessageID, userID)
	if err != nil {
		return fmt.Errorf("failed to update chatwoot message ID: %w", err)
	}
	
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		log.Warn().
			Str("whatsapp_id", whatsappMessageID).
			Int("chatwoot_id", chatwootMessageID).
			Str("user_id", userID).
			Msg("No message found to update with Chatwoot ID")
		return nil
	}
	
	log.Debug().
		Str("whatsapp_id", whatsappMessageID).
		Int("chatwoot_id", chatwootMessageID).
		Str("user_id", userID).
		Int64("rows_affected", rowsAffected).
		Msg("💾 Updated message with Chatwoot ID")
	
	return nil
}

// FindMessageByStanzaID busca mensagem pelo StanzaID (usado para quotes WhatsApp → Chatwoot)
func FindMessageByStanzaID(db *sqlx.DB, stanzaID, userID string) (*MessageRecord, error) {
	var msg MessageRecord
	query := `
		SELECT id, user_id, content, sender_name, message_type, chatwoot_message_id, from_me, chat_jid, created_at, updated_at, chatwoot_conversation_id
		FROM messages 
		WHERE id = $1 AND user_id = $2
		LIMIT 1
	`
	
	err := db.Get(&msg, query, stanzaID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Debug().
				Str("stanza_id", stanzaID).
				Str("user_id", userID).
				Msg("🔍 Message not found by StanzaID")
			return nil, nil // Não encontrada, mas não é erro
		}
		return nil, fmt.Errorf("failed to find message by stanza ID: %w", err)
	}

	log.Debug().
		Str("stanza_id", stanzaID).
		Str("user_id", userID).
		Str("content", truncateString(msg.Content, 50)).
		Bool("has_chatwoot_id", msg.ChatwootMessageID != nil).
		Msg("🔍 Message found by StanzaID")

	return &msg, nil
}

// FindMessageByChatwootID busca mensagem pelo ID do Chatwoot (usado para quotes Chatwoot → WhatsApp)
func FindMessageByChatwootID(db *sqlx.DB, chatwootID int, userID string) (*MessageRecord, error) {
	var msg MessageRecord
	query := `
		SELECT id, user_id, content, sender_name, message_type, chatwoot_message_id, from_me, chat_jid, created_at, updated_at, chatwoot_conversation_id
		FROM messages 
		WHERE chatwoot_message_id = $1 AND user_id = $2
		LIMIT 1
	`
	
	err := db.Get(&msg, query, chatwootID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Debug().
				Int("chatwoot_id", chatwootID).
				Str("user_id", userID).
				Msg("🔍 Message not found by ChatwootID")
			return nil, nil // Não encontrada, mas não é erro
		}
		return nil, fmt.Errorf("failed to find message by chatwoot ID: %w", err)
	}

	log.Debug().
		Int("chatwoot_id", chatwootID).
		Str("user_id", userID).
		Str("content", truncateString(msg.Content, 50)).
		Str("stanza_id", msg.ID).
		Msg("🔍 Message found by ChatwootID")

	return &msg, nil
}

// UpdateChatwootMessageID atualiza o ID da mensagem no Chatwoot após envio bem-sucedido
func UpdateChatwootMessageID(db *sqlx.DB, messageID string, chatwootID int, userID string) error {
	query := `
		UPDATE messages 
		SET chatwoot_message_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ?
	`
	
	result, err := db.Exec(query, chatwootID, messageID, userID)
	if err != nil {
		return fmt.Errorf("failed to update chatwoot message ID: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		log.Warn().
			Str("message_id", messageID).
			Int("chatwoot_id", chatwootID).
			Str("user_id", userID).
			Msg("⚠️ No message found to update with Chatwoot ID")
		return nil // Não é erro fatal
	}

	log.Debug().
		Str("message_id", messageID).
		Int("chatwoot_id", chatwootID).
		Str("user_id", userID).
		Int64("rows_affected", rowsAffected).
		Msg("🔄 Updated message with Chatwoot ID")

	return nil
}

// GetMessageHistory retorna histórico de mensagens para debug/análise (função opcional)
func GetMessageHistory(db *sqlx.DB, userID string, limit int) ([]MessageRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	
	var messages []MessageRecord
	query := `
		SELECT id, user_id, content, sender_name, message_type, chatwoot_message_id, from_me, chat_jid, created_at, updated_at, chatwoot_conversation_id
		FROM messages 
		WHERE user_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	
	err := db.Select(&messages, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get message history: %w", err)
	}

	log.Debug().
		Str("user_id", userID).
		Int("limit", limit).
		Int("found", len(messages)).
		Msg("📚 Retrieved message history")

	return messages, nil
}

// CleanupOldMessages implementa limpeza inteligente de mensagens:
// 1. Conversas ativas: manter últimos 30 dias de mensagens
// 2. Conversas inativas > 90 dias: limpar completamente
func CleanupOldMessages(db *sqlx.DB) error {
	var totalDeleted int64
	
	// Passo 1: Limpar mensagens antigas em conversas ativas
	// (manter só últimos 30 dias da última mensagem por conversa)
	step1Query := `
		DELETE FROM messages m1
		WHERE EXISTS (
			SELECT 1 FROM (
				SELECT chat_jid, user_id, MAX(created_at) as last_msg
				FROM messages 
				GROUP BY chat_jid, user_id
			) recent
			WHERE recent.chat_jid = m1.chat_jid 
			AND recent.user_id = m1.user_id
			AND m1.created_at < (recent.last_msg - INTERVAL '30 days')
		)
	`
	
	result1, err := db.Exec(step1Query)
	if err != nil {
		return fmt.Errorf("failed to cleanup old messages in active conversations: %w", err)
	}

	deleted1, _ := result1.RowsAffected()
	totalDeleted += deleted1

	log.Info().
		Int64("deleted_messages", deleted1).
		Msg("🧹 Step 1: Cleaned old messages in active conversations (30+ days old)")

	// Passo 2: Limpar completamente conversas inativas > 90 dias
	step2Query := `
		DELETE FROM messages
		WHERE (chat_jid, user_id) IN (
			SELECT chat_jid, user_id
			FROM messages
			GROUP BY chat_jid, user_id
			HAVING MAX(created_at) < NOW() - INTERVAL '90 days'
		)
	`
	
	result2, err := db.Exec(step2Query)
	if err != nil {
		return fmt.Errorf("failed to cleanup inactive conversations: %w", err)
	}

	deleted2, _ := result2.RowsAffected()
	totalDeleted += deleted2

	log.Info().
		Int64("deleted_messages", deleted2).
		Msg("🧹 Step 2: Cleaned completely inactive conversations (90+ days)")

	log.Info().
		Int64("total_deleted", totalDeleted).
		Msg("🧹 Cleanup completed successfully")

	return nil
}

// truncateString trunca uma string para logs (função auxiliar)
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}