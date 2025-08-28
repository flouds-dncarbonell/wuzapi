package chatwoot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	"github.com/vincent-petithory/dataurl"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// WebhookPayload representa o payload completo do webhook do Chatwoot
type WebhookPayload struct {
	ID          int    `json:"id"`
	Event       string `json:"event"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
	Private     bool   `json:"private"`
	
	Conversation struct {
		ID       int `json:"id"`
		InboxID  int `json:"inbox_id"`
		ContactInbox struct {
			ID        int    `json:"id"`
			ContactID int    `json:"contact_id"`
			InboxID   int    `json:"inbox_id"`
			SourceID  string `json:"source_id"`
		} `json:"contact_inbox"`
		Meta struct {
			Sender struct {
				Identifier  string `json:"identifier"`
				PhoneNumber string `json:"phone_number"`
			} `json:"sender"`
		} `json:"meta"`
		Messages []struct {
			ID          int          `json:"id"`
			Content     string       `json:"content"`
			Attachments []Attachment `json:"attachments"`
		} `json:"messages"`
	} `json:"conversation"`
	
	Inbox struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"inbox"`
	
	Sender struct {
		ID               int    `json:"id"`
		Name             string `json:"name"`
		AvailableName    string `json:"available_name"`
		Type             string `json:"type"`
	} `json:"sender"`
	
	ContentAttributes struct {
		InReplyTo          int    `json:"in_reply_to"`
		InReplyToExternalID string `json:"in_reply_to_external_id"`
		Deleted            bool   `json:"deleted"`
	} `json:"content_attributes"`
}

// Attachment representa um anexo do Chatwoot
type Attachment struct {
	ID       int    `json:"id"`
	DataURL  string `json:"data_url"`
	FileType string `json:"file_type"`
	FileName string `json:"file_name"`
}

// QuotedMessageInfo representa informações de uma mensagem citada
type QuotedMessageInfo struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	SenderName  string `json:"sender_name"`
	MessageType string `json:"message_type"` // "text", "image", "document", etc.
}

// WebhookContext interface para abstração do contexto HTTP
type WebhookContext interface {
	Param(key string) string
	ShouldBindJSON(obj interface{}) error
	JSON(code int, obj interface{})
}

// WebhookProcessor processa webhooks do Chatwoot para WhatsApp
type WebhookProcessor struct {
	db *sqlx.DB
}

// getWhatsAppClientWithReconnection obtém cliente WhatsApp e detecta desconexão
func (w *WebhookProcessor) getWhatsAppClientWithReconnection(userID string) (*whatsmeow.Client, error) {
	if GlobalClientGetter == nil {
		return nil, fmt.Errorf("GlobalClientGetter não configurado")
	}
	
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		// Detectar desconexão via webhook error
		if GlobalReconnectionManager != nil {
			go GlobalReconnectionManager.EnsureMonitoring(userID)
		}
		return nil, fmt.Errorf("cliente WhatsApp não encontrado para usuário %s", userID)
	}
	
	return client, nil
}

// isSelfConversation verifica se o chatID é uma conversa com o próprio número do bot
func (w *WebhookProcessor) isSelfConversation(chatID, userID string) bool {
	if chatID == "" || userID == "" {
		return false
	}
	
	// Obter número do bot via banco de dados
	var jid string
	err := w.db.Get(&jid, "SELECT jid FROM users WHERE id = $1", userID)
	if err != nil {
		log.Debug().Err(err).Str("userID", userID).Msg("Failed to get bot JID for self-conversation check")
		return false
	}
	
	if jid == "" {
		return false
	}
	
	// Extrair número do JID (formato: 555197173288:23@s.whatsapp.net)
	var botPhone string
	if strings.Contains(jid, "@") {
		phoneNumber := strings.Split(jid, "@")[0]
		// Remover sufixo :23 se existir
		if strings.Contains(phoneNumber, ":") {
			phoneNumber = strings.Split(phoneNumber, ":")[0]
		}
		botPhone = phoneNumber
	}
	
	if botPhone == "" {
		return false
	}
	
	// Verificar se chatID corresponde ao próprio número
	// chatID pode ser: "555197173288@s.whatsapp.net" ou apenas "555197173288"
	chatPhone := chatID
	if strings.Contains(chatID, "@") {
		chatPhone = strings.Split(chatID, "@")[0]
		if strings.Contains(chatPhone, ":") {
			chatPhone = strings.Split(chatPhone, ":")[0]
		}
	}
	
	isSelf := chatPhone == botPhone
	
	log.Debug().
		Str("chatID", chatID).
		Str("botPhone", botPhone).
		Str("chatPhone", chatPhone).
		Bool("isSelf", isSelf).
		Msg("Self-conversation check")
	
	return isSelf
}

// NewWebhookProcessor cria uma nova instância do processador de webhook
func NewWebhookProcessor(db *sqlx.DB) *WebhookProcessor {
	return &WebhookProcessor{
		db: db,
	}
}

// ProcessWebhook processa um webhook recebido do Chatwoot
func (w *WebhookProcessor) ProcessWebhook(c WebhookContext) {
	token := c.Param("token")
	if token == "" {
		log.Error().Msg("Token não fornecido no webhook")
		c.JSON(http.StatusBadRequest, map[string]string{"error": "Token é obrigatório"})
		return
	}

	var payload WebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		log.Error().Err(err).Msg("Erro ao decodificar payload do webhook")
		c.JSON(http.StatusBadRequest, map[string]string{"error": "Payload inválido"})
		return
	}

	// Buscar configuração do Chatwoot para este token
	config, err := GetConfigByToken(w.db, token)
	if err != nil {
		log.Error().Err(err).Msg("Erro ao buscar configuração do Chatwoot")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "Configuração não encontrada"})
		return
	}

	log.Info().
		Str("token", token).
		Str("event", payload.Event).
		Str("message_type", payload.MessageType).
		Int("conversation", payload.Conversation.ID).
		Msg("Webhook recebido do Chatwoot")

	// Verificar se a mensagem deve ser processada
	if !w.shouldProcessMessage(payload, config) {
		log.Debug().Msg("Mensagem ignorada pelos filtros")
		c.JSON(http.StatusOK, map[string]string{"message": "mensagem ignorada"})
		return
	}

	if !config.Enabled {
		log.Warn().Msg("Chatwoot desabilitado para este token")
		c.JSON(http.StatusOK, map[string]string{"message": "chatwoot desabilitado"})
		return
	}

	// Separar o fluxo: typing events vs mensagens normais
	if payload.Event == "conversation_typing_on" || payload.Event == "conversation_typing_off" {
		// Processar apenas typing indicator
		err = w.processTypingEvent(payload, config)
	} else {
		// Processar mensagem normal
		err = w.processMessage(payload, config)
	}
	if err != nil {
		log.Error().Err(err).Msg("Erro ao processar mensagem do webhook")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "Erro ao processar mensagem"})
		return
	}

	c.JSON(http.StatusOK, map[string]string{"message": "webhook processado com sucesso"})
}

// processMessage processa a mensagem do webhook e envia para WhatsApp
func (w *WebhookProcessor) processMessage(payload WebhookPayload, config *Config) error {
	// 1. Verificar formato do identificador
	identifier := payload.Conversation.Meta.Sender.Identifier
	phoneNumber := payload.Conversation.Meta.Sender.PhoneNumber
	
	log.Info().
		Str("identifier", identifier).
		Str("phone_number", phoneNumber).
		Int("conversation", payload.Conversation.ID).
		Str("user_id", config.UserID).
		Msg("Processando mensagem para WhatsApp")
	
	// 2. Se tem identifier válido (JID), processar normalmente
	if w.hasValidJID(identifier) {
		log.Debug().Str("identifier", identifier).
			Msg("✅ Valid JID found - processing normally")
		return w.processWithValidJID(payload, config, identifier)
	}
	
	// 3. Se só tem phone_number, validar
	if phoneNumber != "" {
		cleanPhone := strings.TrimPrefix(phoneNumber, "+")
		log.Info().Str("phone", cleanPhone).
			Msg("🔍 Only phone number found - validating WhatsApp registration")
		return w.validateAndProcess(payload, config, cleanPhone)
	}
	
	// 4. Sem dados suficientes
	log.Error().Msg("❌ No valid identifier or phone number found")
	return fmt.Errorf("no valid identifier or phone number found")

}

// hasValidJID verifica se o identifier é um JID válido do WhatsApp
func (w *WebhookProcessor) hasValidJID(identifier string) bool {
	if identifier == "" {
		return false
	}
	
	// Verificar se é formato JID válido
	return strings.HasSuffix(identifier, "@s.whatsapp.net") || 
		   strings.HasSuffix(identifier, "@g.us")
}

// processWithValidJID processa mensagem com JID válido (fluxo normal)
func (w *WebhookProcessor) processWithValidJID(payload WebhookPayload, config *Config, chatID string) error {
	// Verificar se esta mensagem específica já foi processada (anti-duplicação adicional)
	messageKey := fmt.Sprintf("processed_msg:%d:%s:%s", payload.ID, payload.Event, config.UserID)
	if GlobalCache.HasProcessedReadReceipt(messageKey) {
		log.Debug().
			Int("message_id", payload.ID).
			Str("event", payload.Event).
			Str("chat_id", chatID).
			Msg("Message already processed - skipping")
		return nil
	}

	// Marcar mensagem como processada (TTL 10 minutos)
	GlobalCache.SetCachedData(messageKey, time.Now(), 600) 

	// Aguardar um pouco para evitar problemas de sincronização
	time.Sleep(500 * time.Millisecond)

	// Processar mensagens de deleção
	if payload.Event == "message_updated" && payload.ContentAttributes.Deleted {
		return w.processMessageDeletion(payload, config, chatID)
	}

	// Processar comandos especiais na self-conversation (próprio número)
	if w.isSelfConversation(chatID, config.UserID) && payload.MessageType == "outgoing" {
		return w.processBotCommands(payload, config)
	}

	// Processar mensagens normais outgoing
	if payload.MessageType == "outgoing" && len(payload.Conversation.Messages) > 0 {
		return w.processOutgoingMessages(payload, config, chatID)
	}

	log.Debug().Msg("Nenhum processamento necessário para esta mensagem")
	return nil
}

// validateAndProcess valida número WhatsApp e processa mensagem
func (w *WebhookProcessor) validateAndProcess(payload WebhookPayload, config *Config, phoneNumber string) error {
	log.Info().Str("phone", phoneNumber).Msg("🔍 Validating phone number")
	
	// 1. Validar número via API WhatsApp
	isValid, jid, err := w.validateWhatsAppNumber(phoneNumber, config.UserID)
	if err != nil {
		log.Error().Err(err).Str("phone", phoneNumber).
			Msg("Failed to validate WhatsApp number")
		// Em caso de erro na validação, tentar processar mesmo assim com fallback
		return w.processWithFallback(payload, config, phoneNumber)
	}
	
	if isValid {
		// 2a. Número válido: Processar mensagem + Atualizar contato
		log.Info().Str("phone", phoneNumber).Str("jid", jid).
			Msg("✅ Valid WhatsApp number - processing message and updating contact")
		return w.processValidNumber(payload, config, jid, phoneNumber)
	} else {
		// 2b. Número inválido: Enviar mensagem privada no Chatwoot
		log.Warn().Str("phone", phoneNumber).
			Msg("❌ Invalid WhatsApp number - sending private message to Chatwoot")
		return w.processInvalidNumber(payload, config, phoneNumber)
	}
}

// validateWhatsAppNumber valida se um número tem WhatsApp usando a API IsOnWhatsApp
func (w *WebhookProcessor) validateWhatsAppNumber(phoneNumber, userID string) (bool, string, error) {
	// Usar GlobalClientGetter para validar via IsOnWhatsApp
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		return false, "", fmt.Errorf("WhatsApp client not found for user %s", userID)
	}
	
	log.Debug().Str("phone", phoneNumber).Str("user_id", userID).
		Msg("Calling IsOnWhatsApp API")
	
	// Validar número
	resp, err := client.IsOnWhatsApp([]string{phoneNumber})
	if err != nil {
		return false, "", fmt.Errorf("failed to check WhatsApp: %w", err)
	}
	
	// Verificar resultado
	for _, user := range resp {
		if user.Query == phoneNumber && user.IsIn {
			verifiedName := ""
			if user.VerifiedName != nil && user.VerifiedName.Details != nil {
				verifiedName = user.VerifiedName.Details.GetVerifiedName()
			}
			
			log.Info().
				Str("phone", phoneNumber).
				Str("jid", user.JID.String()).
				Str("verified_name", verifiedName).
				Msg("✅ Number is registered on WhatsApp")
			return true, user.JID.String(), nil // Número válido + JID completo
		}
	}
	
	log.Warn().Str("phone", phoneNumber).Msg("❌ Number is not registered on WhatsApp")
	return false, "", nil // Número não está no WhatsApp
}

// processValidNumber processa número válido: envia mensagem e atualiza contato
func (w *WebhookProcessor) processValidNumber(payload WebhookPayload, config *Config, jid, phoneNumber string) error {
	// 1. Processar mensagem normalmente com JID validado
	err := w.processWithValidJID(payload, config, jid)
	if err != nil {
		log.Error().Err(err).
			Str("jid", jid).
			Str("phone", phoneNumber).
			Msg("Failed to process message with validated JID")
		return err
	}
	
	// 2. Atualizar contato no Chatwoot (em paralelo)
	go w.updateContactInChatwoot(payload.Conversation.ContactInbox.ContactID, jid, config)
	
	log.Info().
		Str("phone", phoneNumber).
		Str("jid", jid).
		Int("contact_id", payload.Conversation.ContactInbox.ContactID).
		Msg("✅ Valid number processed successfully and contact update scheduled")
	
	return nil
}

// processInvalidNumber processa número inválido: envia mensagem privada no Chatwoot
func (w *WebhookProcessor) processInvalidNumber(payload WebhookPayload, config *Config, phoneNumber string) error {
	// Criar cliente Chatwoot
	client := NewClient(*config)
	
	// Criar mensagem privada informando que número é inválido
	invalidMessage := fmt.Sprintf(
		"⚠️ **Número WhatsApp Inválido**\n\n"+
		"O número %s não está registrado no WhatsApp.\n\n"+
		"**Possíveis causas:**\n"+
		"• Número incorreto\n"+
		"• WhatsApp não instalado\n"+
		"• Número bloqueado/inativo\n\n"+
		"_Mensagem não enviada._",
		phoneNumber,
	)
	
	// Criar payload da mensagem privada
	messagePayload := map[string]interface{}{
		"content":      invalidMessage,
		"message_type": "outgoing",
		"private":      true, // 🎯 MENSAGEM PRIVADA - só agentes veem
	}
	
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", 
		config.AccountID, payload.Conversation.ID)
	
	_, err := client.makeRequest("POST", endpoint, messagePayload)
	if err != nil {
		log.Error().Err(err).
			Int("conversation_id", payload.Conversation.ID).
			Str("phone", phoneNumber).
			Msg("Failed to send invalid number notification")
		return err
	}
	
	log.Info().
		Str("phone", phoneNumber).
		Int("conversation_id", payload.Conversation.ID).
		Msg("📝 Private notification sent about invalid WhatsApp number")
	
	return nil // Sucesso - mensagem privada enviada
}

// processWithFallback processa mensagem com fallback quando validação falha
func (w *WebhookProcessor) processWithFallback(payload WebhookPayload, config *Config, phoneNumber string) error {
	// Assumir JID e tentar processar
	assumedJID := phoneNumber + "@s.whatsapp.net"
	
	log.Warn().
		Str("phone", phoneNumber).
		Str("assumed_jid", assumedJID).
		Msg("⚠️ Using fallback JID due to validation error")
	
	return w.processWithValidJID(payload, config, assumedJID)
}

// updateContactInChatwoot atualiza o identifier do contato no Chatwoot com JID validado
func (w *WebhookProcessor) updateContactInChatwoot(contactID int, jid string, config *Config) {
	client := NewClient(*config)
	
	log.Info().Int("contact_id", contactID).Str("jid", jid).
		Msg("🎯 Updating contact with validated JID")
	
	// Preparar payload de atualização
	updatePayload := map[string]interface{}{
		"identifier": jid, // Atualizar identifier com JID completo
		"additional_attributes": map[string]interface{}{
			"whatsapp_validated": true,
			"validation_source":  "wuzapi_webhook",
			"validation_date":    time.Now().Format(time.RFC3339),
		},
	}
	
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d", config.AccountID, contactID)
	
	_, err := client.makeRequest("PATCH", endpoint, updatePayload)
	if err != nil {
		log.Error().Err(err).
			Int("contact_id", contactID).
			Str("jid", jid).
			Msg("Failed to update contact identifier")
		return
	}
	
	log.Info().
		Int("contact_id", contactID).
		Str("jid", jid).
		Msg("✅ Contact identifier updated successfully")
}

// processMessageDeletion processa deleção de mensagens
func (w *WebhookProcessor) processMessageDeletion(payload WebhookPayload, config *Config, chatID string) error {
	log.Info().
		Int("message_id", payload.ID).
		Str("chat_id", chatID).
		Msg("Processando deleção de mensagem")

	// Buscar mensagem no banco pelo chatwoot_message_id
	msg, err := FindMessageByChatwootID(w.db, payload.ID, config.UserID)
	if err != nil {
		log.Error().Err(err).Int("chatwoot_id", payload.ID).Msg("Erro ao buscar mensagem para deleção")
		return fmt.Errorf("failed to find message for deletion: %w", err)
	}

	if msg == nil {
		log.Warn().Int("chatwoot_id", payload.ID).Msg("Mensagem não encontrada no banco para deleção")
		return nil // Não é erro crítico, talvez a mensagem não foi enviada pelo nosso sistema
	}

	// Extrair número do telefone do chatID (remover @s.whatsapp.net ou @g.us)
	phoneNumber := strings.TrimSuffix(strings.TrimSuffix(chatID, "@s.whatsapp.net"), "@g.us")
	
	log.Info().
		Str("whatsapp_message_id", msg.ID).
		Str("phone", phoneNumber).
		Int("chatwoot_id", payload.ID).
		Msg("Deletando mensagem no WhatsApp")

	// Fazer chamada para API interna de deleção
	err = w.deleteMessageFromWhatsApp(msg.ID, phoneNumber, config.UserID)
	if err != nil {
		log.Error().Err(err).Str("message_id", msg.ID).Msg("Erro ao deletar mensagem no WhatsApp")
		return fmt.Errorf("failed to delete message from WhatsApp: %w", err)
	}

	// Remover mensagem do banco local
	err = w.deleteMessageFromDatabase(msg.ID, config.UserID)
	if err != nil {
		log.Error().Err(err).Str("message_id", msg.ID).Msg("Erro ao remover mensagem do banco")
		// Não retornar erro aqui pois a mensagem já foi deletada no WhatsApp
	}

	log.Info().
		Str("message_id", msg.ID).
		Int("chatwoot_id", payload.ID).
		Msg("Mensagem deletada com sucesso")
	
	return nil
}

// deleteMessageFromWhatsApp deleta mensagem diretamente via WhatsApp client (mesmo padrão do envio)
func (w *WebhookProcessor) deleteMessageFromWhatsApp(messageID, phoneNumber, userID string) error {
	// Verificar se GlobalClientGetter está disponível
	if GlobalClientGetter == nil {
		return fmt.Errorf("GlobalClientGetter não configurado")
	}

	// Obter cliente WhatsApp (mesmo padrão usado para enviar mensagens)
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		// Detectar desconexão via webhook error
		if GlobalReconnectionManager != nil {
			go GlobalReconnectionManager.EnsureMonitoring(userID)
		}
		return fmt.Errorf("cliente WhatsApp não encontrado para usuário %s", userID)
	}

	// Parse do número de telefone para JID
	recipient, err := w.parseJID(phoneNumber)
	if err != nil {
		return fmt.Errorf("erro ao fazer parse do telefone %s: %w", phoneNumber, err)
	}

	// Construir mensagem de revogação (mesmo método usado no handler DeleteMessage)
	revokeMessage := client.BuildRevoke(recipient, types.EmptyJID, messageID)
	
	// Enviar mensagem de revogação
	resp, err := client.SendMessage(context.Background(), recipient, revokeMessage)
	if err != nil {
		return fmt.Errorf("erro ao enviar revogação: %w", err)
	}

	log.Info().
		Str("message_id", messageID).
		Str("phone", phoneNumber).
		Str("timestamp", fmt.Sprintf("%v", resp.Timestamp)).
		Msg("Mensagem deletada com sucesso no WhatsApp")

	return nil
}

// deleteMessageFromDatabase remove mensagem do banco local
func (w *WebhookProcessor) deleteMessageFromDatabase(messageID, userID string) error {
	query := `DELETE FROM messages WHERE id = $1 AND user_id = $2`
	
	result, err := w.db.Exec(query, messageID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete message from database: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		log.Warn().Str("message_id", messageID).Msg("Nenhuma linha afetada na deleção do banco")
	} else {
		log.Info().
			Str("message_id", messageID).
			Int64("rows_affected", rowsAffected).
			Msg("Mensagem removida do banco local")
	}

	return nil
}

// processBotCommands processa comandos especiais do bot
func (w *WebhookProcessor) processBotCommands(payload WebhookPayload, config *Config) error {
	// Processar apenas comandos que começam com #
	if !strings.HasPrefix(payload.Content, "#") {
		log.Debug().Str("content", payload.Content).Msg("Not a valid command - must start with #")
		return nil
	}
	
	// Remover prefixo # do comando
	command := strings.TrimPrefix(payload.Content, "#")
	command = strings.ToLower(command)

	log.Info().Str("command", command).Msg("Processando comando do bot")

	switch {
	case command == "help" || command == "ajuda":
		return w.handleHelpCommand(payload, config)
		
	case command == "qrcode" || command == "qr":
		return w.handleQRCodeCommand(payload, config)
		
	case command == "status":
		return w.handleStatusCommand(payload, config)
		
	case strings.Contains(command, "init") || strings.Contains(command, "iniciar"):
		return w.handleInitCommand(payload, config)
		
	case command == "clearcache" || command == "limpar":
		return w.handleClearCacheCommand(payload, config)
		
	case command == "disconnect" || command == "desconectar":
		return w.handleDisconnectCommand(payload, config)
		
	default:
		// Comando não reconhecido - enviar sugestão
		return w.handleUnknownCommand(payload, config, command)
	}

	return nil
}

// processOutgoingMessages processa mensagens outgoing normais
func (w *WebhookProcessor) processOutgoingMessages(payload WebhookPayload, config *Config, chatID string) error {
	log.Info().
		Str("chat_id", chatID).
		Int("messages", len(payload.Conversation.Messages)).
		Msg("Processando mensagens outgoing")

	// Cache automático do source_id do webhook outgoing
	if payload.Conversation.ContactInbox.SourceID != "" {
		cacheKey := fmt.Sprintf("source_id_conv_%d", payload.Conversation.ID)
		GlobalCache.SetCachedData(cacheKey, payload.Conversation.ContactInbox.SourceID, 3600)
		
		log.Debug().
			Int("conversation_id", payload.Conversation.ID).
			Str("source_id", payload.Conversation.ContactInbox.SourceID).
			Msg("Cached source_id from webhook outgoing")
	}

	for _, message := range payload.Conversation.Messages {
		err := w.processSingleMessage(message, payload, config, chatID)
		if err != nil {
			log.Error().Err(err).Int("message_id", message.ID).Msg("Erro ao processar mensagem individual")
			// Continuar processando outras mensagens mesmo se uma falhar
		}
	}

	return nil
}

// processSingleMessage processa uma única mensagem
func (w *WebhookProcessor) processSingleMessage(message struct {
	ID          int          `json:"id"`
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments"`
}, payload WebhookPayload, config *Config, chatID string) error {

	// Verificar se mensagem já existe no banco (prevenção de duplicação)
	existingMsg, err := FindMessageByChatwootID(w.db, message.ID, config.UserID)
	if err != nil {
		log.Error().Err(err).
			Int("chatwoot_id", message.ID).
			Str("user_id", config.UserID).
			Msg("Error checking if message exists in database")
		// Continuar processamento em caso de erro na consulta
	} else if existingMsg != nil {
		log.Debug().
			Int("chatwoot_id", message.ID).
			Str("existing_whatsapp_id", existingMsg.ID).
			Str("chat_id", chatID).
			Str("sender_name", existingMsg.SenderName).
			Bool("from_me", existingMsg.FromMe).
			Msg("🔄 Message already exists in database - skipping webhook processing to prevent duplication")
		return nil
	}

	// Converter formatação markdown para WhatsApp
	content := w.convertMarkdownToWhatsApp(message.Content)

	// Adicionar assinatura do agente se habilitado
	if config.SignMsg && payload.Sender.Name != "" {
		delimiter := config.SignDelimiter
		if delimiter == "" {
			delimiter = "\n"
		}
		// Substituir \\n por quebra de linha real
		delimiter = strings.ReplaceAll(delimiter, "\\n", "\n")
		
		content = fmt.Sprintf("*%s:*%s%s", payload.Sender.Name, delimiter, content)
	}

	// Verificar se é uma mensagem citada (reply)
	var quotedMessage *QuotedMessageInfo
	if payload.ContentAttributes.InReplyTo != 0 || payload.ContentAttributes.InReplyToExternalID != "" {
		var err error
		quotedMessage, err = w.getQuotedMessage(payload.ContentAttributes, config.UserID)
		if err != nil {
			log.Warn().Err(err).Msg("Erro ao obter mensagem citada, continuando sem quote")
		}
	}

	// Se houver anexos, processar separadamente
	if len(message.Attachments) > 0 {
		for _, attachment := range message.Attachments {
			err := w.processAttachmentWithQuote(attachment, content, config, chatID, quotedMessage, &message.ID)
			if err != nil {
				return fmt.Errorf("erro ao processar anexo: %w", err)
			}
			// Limpar content após primeiro anexo para não repetir
			content = ""
		}
		return nil
	}

	// Enviar mensagem de texto simples
	if content != "" {
		return w.sendTextMessageWithQuote(content, config, chatID, quotedMessage, &message.ID)
	}

	return nil
}

// processAttachment processa um anexo
func (w *WebhookProcessor) processAttachment(attachment Attachment, caption string, config *Config, chatID string) error {
	log.Info().
		Int("attachment_id", attachment.ID).
		Str("file_type", attachment.FileType).
		Str("file_name", attachment.FileName).
		Str("data_url", attachment.DataURL[:min(50, len(attachment.DataURL))]+"...").
		Str("chat_id", chatID).
		Msg("Processando anexo")

	// Baixar o arquivo do Chatwoot
	fileData, mimeType, err := w.downloadAttachment(attachment.DataURL)
	if err != nil {
		return fmt.Errorf("erro ao baixar anexo: %w", err)
	}

	// Determinar tipo de mídia baseado no MIME type
	mediaType := w.getMediaType(mimeType, attachment.FileType)
	
	log.Info().
		Str("mime_type", mimeType).
		Str("media_type", mediaType).
		Int("file_size", len(fileData)).
		Msg("Arquivo baixado, enviando para WhatsApp")

	// Enviar baseado no tipo de mídia
	switch mediaType {
	case "image":
		return w.sendImage(fileData, mimeType, caption, config, chatID, attachment.FileName)
	case "video":
		return w.sendVideo(fileData, mimeType, caption, config, chatID, attachment.FileName)
	case "audio":
		return w.sendAudio(fileData, mimeType, config, chatID, attachment.FileName)
	case "document":
		return w.sendDocument(fileData, mimeType, caption, config, chatID, attachment.FileName)
	default:
		// Fallback para documento
		return w.sendDocument(fileData, mimeType, caption, config, chatID, attachment.FileName)
	}
}

// downloadAttachment baixa um anexo do Chatwoot
func (w *WebhookProcessor) downloadAttachment(dataURL string) ([]byte, string, error) {
	// Se é data URL (base64), decodificar diretamente
	if strings.HasPrefix(dataURL, "data:") {
		data, err := dataurl.DecodeString(dataURL)
		if err != nil {
			return nil, "", fmt.Errorf("erro ao decodificar data URL: %w", err)
		}
		return data.Data, data.MediaType.ContentType(), nil
	}

	// Se é URL HTTP, baixar o arquivo
	resp, err := http.Get(dataURL)
	if err != nil {
		return nil, "", fmt.Errorf("erro ao fazer requisição HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("erro HTTP: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("erro ao ler dados: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return data, contentType, nil
}

// getMediaType determina o tipo de mídia baseado no MIME type
func (w *WebhookProcessor) getMediaType(mimeType, fileType string) string {
	// Usar fileType do Chatwoot se disponível
	if fileType != "" {
		switch fileType {
		case "image":
			return "image"
		case "video":
			return "video"
		case "audio":
			return "audio"
		default:
			return "document"
		}
	}

	// Fallback para MIME type
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "document"
	}
}

// sendImage envia uma imagem para o WhatsApp
func (w *WebhookProcessor) sendImage(data []byte, mimeType, caption string, config *Config, chatID, fileName string) error {
	client, err := w.getWhatsAppClientWithReconnection(config.UserID)
	if err != nil {
		return err
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload da imagem
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload da imagem: %w", err)
	}

	// Criar mensagem de imagem
	imageMsg := &waE2E.ImageMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}

	if caption != "" {
		imageMsg.Caption = &caption
	}

	message := &waE2E.Message{
		ImageMessage: imageMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar imagem: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Msg("Imagem enviada com sucesso")

	// Salvar mensagem no banco de dados local
	if err := w.saveOutgoingMessage(resp.ID, caption, config.UserID, chatID, "image", nil); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Msg("Failed to save outgoing image message")
	}

	return nil
}

// sendVideo envia um vídeo para o WhatsApp
func (w *WebhookProcessor) sendVideo(data []byte, mimeType, caption string, config *Config, chatID, fileName string) error {
	client, err := w.getWhatsAppClientWithReconnection(config.UserID)
	if err != nil {
		return err
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload do vídeo
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload do vídeo: %w", err)
	}

	// Criar mensagem de vídeo
	videoMsg := &waE2E.VideoMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}

	if caption != "" {
		videoMsg.Caption = &caption
	}

	message := &waE2E.Message{
		VideoMessage: videoMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar vídeo: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Msg("Vídeo enviado com sucesso")

	// Salvar mensagem no banco de dados local
	if err := w.saveOutgoingMessage(resp.ID, caption, config.UserID, chatID, "video", nil); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Msg("Failed to save outgoing video message")
	}

	return nil
}

// sendAudio envia um áudio para o WhatsApp
func (w *WebhookProcessor) sendAudio(data []byte, mimeType string, config *Config, chatID, fileName string) error {
	client, err := w.getWhatsAppClientWithReconnection(config.UserID)
	if err != nil {
		return err
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload do áudio
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload do áudio: %w", err)
	}

	// Criar mensagem de áudio
	audioMsg := &waE2E.AudioMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}

	message := &waE2E.Message{
		AudioMessage: audioMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar áudio: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Msg("Áudio enviado com sucesso")

	// Salvar mensagem no banco de dados local
	if err := w.saveOutgoingMessage(resp.ID, "", config.UserID, chatID, "audio", nil); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Msg("Failed to save outgoing audio message")
	}

	return nil
}

// sendDocument envia um documento para o WhatsApp
func (w *WebhookProcessor) sendDocument(data []byte, mimeType, caption string, config *Config, chatID, fileName string) error {
	client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if client == nil {
		return fmt.Errorf("cliente WhatsApp não encontrado")
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload do documento
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload do documento: %w", err)
	}

	// Usar nome do arquivo se disponível, senão gerar um
	if fileName == "" {
		ext := filepath.Ext(mimeType)
		if ext == "" {
			ext = ".bin"
		}
		fileName = fmt.Sprintf("document%s", ext)
	}

	// Criar mensagem de documento
	docMsg := &waE2E.DocumentMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
		FileName:      &fileName,
	}

	message := &waE2E.Message{
		DocumentMessage: docMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar documento: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Str("file_name", fileName).
		Msg("Documento enviado com sucesso")

	// Salvar mensagem de documento no banco
	if err := w.saveOutgoingMessage(resp.ID, fileName, config.UserID, chatID, "document", nil); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Msg("Failed to save outgoing document message")
	}

	// Se houver caption, enviar como mensagem separada
	if caption != "" {
		time.Sleep(100 * time.Millisecond) // Pequeno delay
		textMsg := &waE2E.Message{
			Conversation: &caption,
		}
		captionMessageID := client.GenerateMessageID()
		captionResp, err := client.SendMessage(context.Background(), jid, textMsg, whatsmeow.SendRequestExtra{ID: captionMessageID})
		if err != nil {
			log.Warn().Err(err).Msg("Erro ao enviar caption do documento")
		} else {
			// Salvar caption separadamente
			if err := w.saveOutgoingMessage(captionResp.ID, caption, config.UserID, chatID, "text", nil); err != nil {
				log.Warn().Err(err).
					Str("message_id", captionResp.ID).
					Msg("Failed to save document caption message")
			}
		}
	}

	return nil
}

// getQuotedMessage busca informações da mensagem citada
func (w *WebhookProcessor) getQuotedMessage(contentAttrs struct {
	InReplyTo          int    `json:"in_reply_to"`
	InReplyToExternalID string `json:"in_reply_to_external_id"`
	Deleted            bool   `json:"deleted"`
}, userID string) (*QuotedMessageInfo, error) {
	
	log.Debug().
		Int("in_reply_to", contentAttrs.InReplyTo).
		Str("in_reply_to_external_id", contentAttrs.InReplyToExternalID).
		Msg("Buscando mensagem citada")

	// Priorizar external ID (ID da mensagem original do WhatsApp)
	if contentAttrs.InReplyToExternalID != "" {
		return w.findMessageByExternalID(contentAttrs.InReplyToExternalID, userID)
	}

	// Fallback para ID interno do Chatwoot
	if contentAttrs.InReplyTo != 0 {
		return w.findMessageByChatwootID(strconv.Itoa(contentAttrs.InReplyTo))
	}

	return nil, fmt.Errorf("nenhum ID de mensagem citada fornecido")
}

// findMessageByExternalID busca mensagem pelo ID externo (WhatsApp)
func (w *WebhookProcessor) findMessageByExternalID(externalID, userID string) (*QuotedMessageInfo, error) {
	log.Debug().
		Str("external_id", externalID).
		Str("user_id", userID).
		Msg("🔍 Buscando mensagem citada por External ID")

	// Buscar mensagem no banco usando a função implementada
	msg, err := FindMessageByStanzaID(w.db, externalID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to find message by external ID: %w", err)
	}
	
	// Se não encontrou mensagem, retornar placeholder
	if msg == nil {
		log.Warn().
			Str("external_id", externalID).
			Str("user_id", userID).
			Msg("⚠️ Message not found in database - using placeholder")
		
		return &QuotedMessageInfo{
			ID:          externalID,
			Content:     "[Mensagem não encontrada]",
			SenderName:  "Usuário",
			MessageType: "text",
		}, nil
	}
	
	// Retornar dados reais da mensagem encontrada
	log.Info().
		Str("external_id", externalID).
		Str("content", truncateString(msg.Content, 50)).
		Str("sender", msg.SenderName).
		Str("type", msg.MessageType).
		Msg("✅ Found real quoted message content")
	
	return &QuotedMessageInfo{
		ID:          msg.ID,
		Content:     msg.Content,
		SenderName:  msg.SenderName,
		MessageType: msg.MessageType,
	}, nil
}

// findMessageByChatwootID busca mensagem pelo ID do Chatwoot
func (w *WebhookProcessor) findMessageByChatwootID(chatwootID string) (*QuotedMessageInfo, error) {
	log.Debug().
		Str("chatwoot_id", chatwootID).
		Msg("🔍 Buscando mensagem citada por Chatwoot ID")

	// Converter chatwootID string para int
	chatwootIDInt, err := strconv.Atoi(chatwootID)
	if err != nil {
		return nil, fmt.Errorf("invalid chatwoot ID format: %w", err)
	}

	// Buscar mensagem no banco pelo Chatwoot ID
	// Nota: Precisamos do userID, mas não temos no contexto do webhook
	// Por enquanto, buscar sem filtro de usuário (pode retornar resultados de outros usuários)
	// TODO: Melhorar para incluir userID no contexto do webhook
	
	var msg *MessageRecord
	query := `
		SELECT id, user_id, content, sender_name, message_type, chatwoot_message_id, from_me, chat_jid, created_at
		FROM messages 
		WHERE chatwoot_message_id = $1
		LIMIT 1
	`
	
	var msgRecord MessageRecord
	err = w.db.Get(&msgRecord, query, chatwootIDInt)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Warn().
				Str("chatwoot_id", chatwootID).
				Msg("⚠️ Message not found by Chatwoot ID - using placeholder")
			
			return &QuotedMessageInfo{
				ID:          chatwootID,
				Content:     "[Mensagem não encontrada]",
				SenderName:  "Agente",
				MessageType: "text",
			}, nil
		}
		return nil, fmt.Errorf("failed to find message by chatwoot ID: %w", err)
	}
	
	msg = &msgRecord
	
	// Retornar dados reais da mensagem encontrada
	log.Info().
		Str("chatwoot_id", chatwootID).
		Str("stanza_id", msg.ID).
		Str("content", truncateString(msg.Content, 50)).
		Str("sender", msg.SenderName).
		Msg("✅ Found real message content by Chatwoot ID")
	
	return &QuotedMessageInfo{
		ID:          msg.ID, // Usar StanzaID para o WhatsApp
		Content:     msg.Content,
		SenderName:  msg.SenderName,
		MessageType: msg.MessageType,
	}, nil
}

// sendTextMessageWithQuote envia mensagem de texto com quote
func (w *WebhookProcessor) sendTextMessageWithQuote(content string, config *Config, chatID string, quotedMessage *QuotedMessageInfo, chatwootMessageID *int) error {
	log.Info().
		Str("chat_id", chatID).
		Str("user_id", config.UserID).
		Str("content", content).
		Bool("has_quote", quotedMessage != nil).
		Msg("Enviando mensagem de texto para WhatsApp")

	// Obter o cliente WhatsApp do usuário
	if GlobalClientGetter == nil {
		return fmt.Errorf("GlobalClientGetter não configurado")
	}

	client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if client == nil {
		// Detectar desconexão via webhook error
		if GlobalReconnectionManager != nil {
			go GlobalReconnectionManager.EnsureMonitoring(config.UserID)
		}
		return fmt.Errorf("cliente WhatsApp não encontrado para usuário %s", config.UserID)
	}

	if !client.IsConnected() {
		return fmt.Errorf("cliente WhatsApp não conectado para usuário %s", config.UserID)
	}

	// Converter chatID para JID do WhatsApp
	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID para JID: %w", err)
	}

	// Criar mensagem
	var message *waE2E.Message

	if quotedMessage != nil {
		// Mensagem com quote
		message = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: &content,
				ContextInfo: &waE2E.ContextInfo{
					StanzaID:    &quotedMessage.ID,
					Participant: func() *string { s := jid.String(); return &s }(), // JID do participante original
					QuotedMessage: &waE2E.Message{
						Conversation: &quotedMessage.Content,
					},
				},
			},
		}
	} else {
		// Mensagem simples
		message = &waE2E.Message{
			Conversation: &content,
		}
	}

	// Gerar ID da mensagem
	messageID := client.GenerateMessageID()

	// Enviar mensagem
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{
		ID: messageID,
	})
	
	if err != nil {
		log.Error().Err(err).Msg("Erro ao enviar mensagem para WhatsApp")
		return fmt.Errorf("erro ao enviar mensagem: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Int64("timestamp", resp.Timestamp.Unix()).
		Str("chat_id", chatID).
		Bool("quoted", quotedMessage != nil).
		Bool("success", true).
		Msg("Mensagem enviada com sucesso para WhatsApp")

	// Salvar mensagem no banco de dados local para permitir replies bidirecionais
	if err := w.saveOutgoingMessage(resp.ID, content, config.UserID, chatID, "text", chatwootMessageID); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Str("chat_id", chatID).
			Interface("chatwoot_message_id", chatwootMessageID).
			Msg("Failed to save outgoing message to database - continuing anyway")
	}

	return nil
}

// processAttachmentWithQuote processa anexo com suporte a quote
func (w *WebhookProcessor) processAttachmentWithQuote(attachment Attachment, caption string, config *Config, chatID string, quotedMessage *QuotedMessageInfo, chatwootMessageID *int) error {
	log.Info().
		Int("attachment_id", attachment.ID).
		Str("file_type", attachment.FileType).
		Str("file_name", attachment.FileName).
		Bool("has_quote", quotedMessage != nil).
		Str("chat_id", chatID).
		Msg("Processando anexo com quote")

	// Baixar o arquivo do Chatwoot
	fileData, mimeType, err := w.downloadAttachment(attachment.DataURL)
	if err != nil {
		return fmt.Errorf("erro ao baixar anexo: %w", err)
	}

	// Determinar tipo de mídia baseado no MIME type
	mediaType := w.getMediaType(mimeType, attachment.FileType)

	// Enviar baseado no tipo de mídia - as funções de mídia já suportam ContextInfo
	switch mediaType {
	case "image":
		return w.sendImageWithQuote(fileData, mimeType, caption, config, chatID, attachment.FileName, quotedMessage, chatwootMessageID)
	case "video":
		return w.sendVideoWithQuote(fileData, mimeType, caption, config, chatID, attachment.FileName, quotedMessage, chatwootMessageID)
	case "audio":
		return w.sendAudioWithQuote(fileData, mimeType, config, chatID, attachment.FileName, quotedMessage, chatwootMessageID)
	case "document":
		return w.sendDocumentWithQuote(fileData, mimeType, caption, config, chatID, attachment.FileName, quotedMessage, chatwootMessageID)
	default:
		return w.sendDocumentWithQuote(fileData, mimeType, caption, config, chatID, attachment.FileName, quotedMessage, chatwootMessageID)
	}
}

// sendImageWithQuote envia imagem com quote
func (w *WebhookProcessor) sendImageWithQuote(data []byte, mimeType, caption string, config *Config, chatID, fileName string, quotedMessage *QuotedMessageInfo, chatwootMessageID *int) error {
	client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if client == nil {
		return fmt.Errorf("cliente WhatsApp não encontrado")
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload da imagem
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload da imagem: %w", err)
	}

	// Criar mensagem de imagem
	imageMsg := &waE2E.ImageMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}

	if caption != "" {
		imageMsg.Caption = &caption
	}

	// Adicionar quote se disponível
	if quotedMessage != nil {
		imageMsg.ContextInfo = &waE2E.ContextInfo{
			StanzaID:    &quotedMessage.ID,
			Participant: func() *string { s := jid.String(); return &s }(),
			QuotedMessage: &waE2E.Message{
				Conversation: &quotedMessage.Content,
			},
		}
	}

	message := &waE2E.Message{
		ImageMessage: imageMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar imagem: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Bool("has_quote", quotedMessage != nil).
		Msg("Imagem com quote enviada com sucesso")

	// Salvar mensagem no banco de dados local
	if err := w.saveOutgoingMessage(resp.ID, caption, config.UserID, chatID, "image", chatwootMessageID); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Interface("chatwoot_message_id", chatwootMessageID).
			Msg("Failed to save outgoing image message with quote")
	}

	return nil
}

// sendVideoWithQuote, sendAudioWithQuote, sendDocumentWithQuote seguem padrão similar
// Por brevidade, implementarei apenas os placeholders com fallback para as versões sem quote

func (w *WebhookProcessor) sendVideoWithQuote(data []byte, mimeType, caption string, config *Config, chatID, fileName string, quotedMessage *QuotedMessageInfo, chatwootMessageID *int) error {
	client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if client == nil {
		return fmt.Errorf("cliente WhatsApp não encontrado")
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload do vídeo
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload do vídeo: %w", err)
	}

	// Criar mensagem de vídeo
	videoMsg := &waE2E.VideoMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}

	if caption != "" {
		videoMsg.Caption = &caption
	}

	// Adicionar quote se disponível
	if quotedMessage != nil {
		videoMsg.ContextInfo = &waE2E.ContextInfo{
			StanzaID:    &quotedMessage.ID,
			Participant: func() *string { s := jid.String(); return &s }(),
			QuotedMessage: &waE2E.Message{
				Conversation: &quotedMessage.Content,
			},
		}
	}

	message := &waE2E.Message{
		VideoMessage: videoMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar vídeo: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Bool("has_quote", quotedMessage != nil).
		Msg("Vídeo com quote enviado com sucesso")

	// Salvar mensagem no banco de dados local
	if err := w.saveOutgoingMessage(resp.ID, caption, config.UserID, chatID, "video", chatwootMessageID); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Interface("chatwoot_message_id", chatwootMessageID).
			Msg("Failed to save outgoing video message with quote")
	}

	return nil
}

func (w *WebhookProcessor) sendAudioWithQuote(data []byte, mimeType string, config *Config, chatID, fileName string, quotedMessage *QuotedMessageInfo, chatwootMessageID *int) error {
	client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if client == nil {
		return fmt.Errorf("cliente WhatsApp não encontrado")
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload do áudio
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload do áudio: %w", err)
	}

	// Criar mensagem de áudio
	audioMsg := &waE2E.AudioMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}

	// Adicionar quote se disponível
	if quotedMessage != nil {
		audioMsg.ContextInfo = &waE2E.ContextInfo{
			StanzaID:    &quotedMessage.ID,
			Participant: func() *string { s := jid.String(); return &s }(),
			QuotedMessage: &waE2E.Message{
				Conversation: &quotedMessage.Content,
			},
		}
	}

	message := &waE2E.Message{
		AudioMessage: audioMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar áudio: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Bool("has_quote", quotedMessage != nil).
		Msg("Áudio com quote enviado com sucesso")

	// Salvar mensagem no banco de dados local
	if err := w.saveOutgoingMessage(resp.ID, fileName, config.UserID, chatID, "audio", chatwootMessageID); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Interface("chatwoot_message_id", chatwootMessageID).
			Msg("Failed to save outgoing audio message with quote")
	}

	return nil
}

func (w *WebhookProcessor) sendDocumentWithQuote(data []byte, mimeType, caption string, config *Config, chatID, fileName string, quotedMessage *QuotedMessageInfo, chatwootMessageID *int) error {
	client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if client == nil {
		return fmt.Errorf("cliente WhatsApp não encontrado")
	}

	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID: %w", err)
	}

	// Upload do documento
	uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("erro ao fazer upload do documento: %w", err)
	}

	// Criar mensagem de documento
	docMsg := &waE2E.DocumentMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimeType,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
		FileName:      &fileName,
	}

	// Adicionar quote se disponível
	if quotedMessage != nil {
		docMsg.ContextInfo = &waE2E.ContextInfo{
			StanzaID:    &quotedMessage.ID,
			Participant: func() *string { s := jid.String(); return &s }(),
			QuotedMessage: &waE2E.Message{
				Conversation: &quotedMessage.Content,
			},
		}
	}

	message := &waE2E.Message{
		DocumentMessage: docMsg,
	}

	messageID := client.GenerateMessageID()
	resp, err := client.SendMessage(context.Background(), jid, message, whatsmeow.SendRequestExtra{ID: messageID})
	
	if err != nil {
		return fmt.Errorf("erro ao enviar documento: %w", err)
	}

	log.Info().
		Str("message_id", resp.ID).
		Str("chat_id", chatID).
		Str("file_name", fileName).
		Bool("has_quote", quotedMessage != nil).
		Msg("Documento com quote enviado com sucesso")

	// Salvar mensagem de documento no banco
	if err := w.saveOutgoingMessage(resp.ID, fileName, config.UserID, chatID, "document", chatwootMessageID); err != nil {
		log.Warn().Err(err).
			Str("message_id", resp.ID).
			Interface("chatwoot_message_id", chatwootMessageID).
			Msg("Failed to save outgoing document message with quote")
	}

	// Se houver caption, enviar como mensagem separada com quote
	if caption != "" {
		time.Sleep(100 * time.Millisecond) // Pequeno delay
		
		// Para o caption, usar o mesmo quotedMessage se disponível
		textMsg := &waE2E.Message{
			Conversation: &caption,
		}
		
		// Adicionar quote ao caption também se disponível
		if quotedMessage != nil {
			textMsg = &waE2E.Message{
				ExtendedTextMessage: &waE2E.ExtendedTextMessage{
					Text: &caption,
					ContextInfo: &waE2E.ContextInfo{
						StanzaID:    &quotedMessage.ID,
						Participant: func() *string { s := jid.String(); return &s }(),
						QuotedMessage: &waE2E.Message{
							Conversation: &quotedMessage.Content,
						},
					},
				},
			}
		}
		
		captionMessageID := client.GenerateMessageID()
		captionResp, err := client.SendMessage(context.Background(), jid, textMsg, whatsmeow.SendRequestExtra{ID: captionMessageID})
		if err != nil {
			log.Warn().Err(err).Msg("Erro ao enviar caption do documento com quote")
		} else {
			// Salvar caption separadamente (sem chatwoot_message_id pois é mensagem adicional)
			if err := w.saveOutgoingMessage(captionResp.ID, caption, config.UserID, chatID, "text", nil); err != nil {
				log.Warn().Err(err).
					Str("message_id", captionResp.ID).
					Msg("Failed to save document caption message with quote")
			}
		}
	}

	return nil
}

// sendTextMessage envia uma mensagem de texto para o WhatsApp (versão sem quote)
func (w *WebhookProcessor) sendTextMessage(content string, config *Config, chatID string) error {
	return w.sendTextMessageWithQuote(content, config, chatID, nil, nil)
}

// parseJID converte um chatID em JID do WhatsApp
func (w *WebhookProcessor) parseJID(chatID string) (types.JID, error) {
	// Se o chatID já contém @, usar como está
	if strings.Contains(chatID, "@") {
		jid, err := types.ParseJID(chatID)
		if err != nil {
			return types.EmptyJID, fmt.Errorf("JID inválido: %w", err)
		}
		return jid, nil
	}

	// Se é só números, determinar se é grupo ou contato individual
	if len(chatID) > 15 {
		// IDs longos são provavelmente grupos
		return types.NewJID(chatID, types.GroupServer), nil
	} else {
		// IDs curtos são números de telefone individuais
		return types.NewJID(chatID, types.DefaultUserServer), nil
	}
}

// logWebhookPayload faz log detalhado do payload recebido
func (w *WebhookProcessor) logWebhookPayload(payload WebhookPayload) {
	payloadJSON, _ := json.MarshalIndent(payload, "", "  ")
	log.Debug().Str("payload", string(payloadJSON)).Msg("Payload do webhook detalhado")
}

// convertMarkdownToWhatsApp converte formatação markdown do Chatwoot para WhatsApp
// Parser manual para evitar regex com lookahead/lookbehind não suportado pelo Go RE2
func (w *WebhookProcessor) convertMarkdownToWhatsApp(content string) string {
	if content == "" {
		return ""
	}

	var result strings.Builder
	runes := []rune(content)
	i := 0
	
	for i < len(runes) {
		// 1. Negrito: **texto** → *texto*
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			if end := w.findClosingMark(runes, i+2, "**"); end != -1 {
				result.WriteRune('*')
				result.WriteString(string(runes[i+2:end]))
				result.WriteRune('*')
				i = end + 2
				continue
			}
		}
		
		// 2. Itálico: *texto* → _texto_ (apenas se não for negrito)
		if runes[i] == '*' && (i == 0 || runes[i-1] != '*') && 
		   (i+1 >= len(runes) || runes[i+1] != '*') {
			if end := w.findClosingMark(runes, i+1, "*"); end != -1 {
				result.WriteRune('_')
				result.WriteString(string(runes[i+1:end]))
				result.WriteRune('_')
				i = end + 1
				continue
			}
		}
		
		// 3. Itálico: _texto_ → _texto_ (mantém)
		if runes[i] == '_' && (i == 0 || runes[i-1] != '_') {
			if end := w.findClosingMark(runes, i+1, "_"); end != -1 {
				result.WriteRune('_')
				result.WriteString(string(runes[i+1:end]))
				result.WriteRune('_')
				i = end + 1
				continue
			}
		}
		
		// 4. Riscado: ~~texto~~ → ~texto~
		if i+1 < len(runes) && runes[i] == '~' && runes[i+1] == '~' {
			if end := w.findClosingMark(runes, i+2, "~~"); end != -1 {
				result.WriteRune('~')
				result.WriteString(string(runes[i+2:end]))
				result.WriteRune('~')
				i = end + 2
				continue
			}
		}
		
		// 5. Monoespaço: `texto` → ```texto``` 
		if runes[i] == '`' && (i == 0 || runes[i-1] != '`') {
			if end := w.findClosingMark(runes, i+1, "`"); end != -1 && 
			   (end+1 >= len(runes) || runes[end+1] != '`') {
				result.WriteString("```")
				result.WriteString(string(runes[i+1:end]))
				result.WriteString("```")
				i = end + 1
				continue
			}
		}
		
		// Caractere comum - copia diretamente
		result.WriteRune(runes[i])
		i++
	}

	return result.String()
}

// findClosingMark encontra a marca de fechamento correspondente
func (w *WebhookProcessor) findClosingMark(runes []rune, start int, mark string) int {
	markRunes := []rune(mark)
	markLen := len(markRunes)
	
	for i := start; i <= len(runes)-markLen; i++ {
		// Não aceita quebras de linha no meio
		if runes[i] == '\n' {
			return -1
		}
		
		// Verifica se encontrou a marca de fechamento
		match := true
		for j := 0; j < markLen; j++ {
			if i+j >= len(runes) || runes[i+j] != markRunes[j] {
				match = false
				break
			}
		}
		
		if match {
			// Verifica se há conteúdo entre abertura e fechamento
			if i > start {
				return i
			}
		}
	}
	
	return -1
}

// Exemplos de conversão para teste:
// Input: "**Negrito** e *itálico* e ~~riscado~~ e `código`"
// Output: "*Negrito* e _itálico_ e ~riscado~ e ```código```"
//
// Input: "Lista:\n- Item 1\n- Item 2\n1. Numerado\n> Citação"  
// Output: "Lista:\n- Item 1\n- Item 2\n1. Numerado\n> Citação" (sem mudança)
//
// Input: "***Negrito e itálico***"
// Output: "_*Negrito e itálico*_" (combinação de estilos)

// extractChatID extrai o ID do chat do payload do webhook
func (w *WebhookProcessor) extractChatID(payload WebhookPayload) string {
	// Prioridade: identifier > phone_number limpo
	if payload.Conversation.Meta.Sender.Identifier != "" {
		return payload.Conversation.Meta.Sender.Identifier
	}
	
	if payload.Conversation.Meta.Sender.PhoneNumber != "" {
		// Remove + do phone number
		return strings.TrimPrefix(payload.Conversation.Meta.Sender.PhoneNumber, "+")
	}
	
	return ""
}

// shouldProcessMessage verifica se a mensagem deve ser processada
func (w *WebhookProcessor) shouldProcessMessage(payload WebhookPayload, config *Config) bool {
	// Não processar mensagens privadas
	if payload.Private {
		return false
	}
	
	// IMPORTANTE: Processar apenas eventos específicos para evitar duplicação
	allowedEvents := map[string]bool{
		"message_created": true,  // Mensagem criada → DEVE enviar
		// "message_updated": false,  // Mensagem editada → NÃO enviar (evita duplicação)
		"conversation_typing_on": true,  // Digitando iniciado → Enviar presence
		"conversation_typing_off": true, // Digitando parado → Enviar presence
	}
	
	// Permitir message_updated APENAS para deleções
	if payload.Event == "message_updated" && payload.ContentAttributes.Deleted {
		log.Debug().Msg("Allowing message_updated event for deletion")
		// Continuar processamento para deleção
	} else if !allowedEvents[payload.Event] {
		log.Debug().
			Str("event", payload.Event).
			Str("message_type", payload.MessageType).
			Msg("Event ignored - not in allowed list")
		return false
	}
	
	// Para eventos de typing, não exigir MessageType "outgoing"
	if payload.Event == "conversation_typing_on" || payload.Event == "conversation_typing_off" {
		// Typing events são permitidos independente do MessageType
		return payload.Conversation.ID != 0
	}
	
	// Não processar se não for mensagem outgoing (para outros eventos)
	if payload.MessageType != "outgoing" {
		return false
	}
	
	// Não processar se não houver conversa
	if payload.Conversation.ID == 0 {
		return false
	}
	
	// Comandos do bot (self-conversation) são permitidos mesmo em outros eventos
	chatID := w.extractChatID(payload)
	if w.isSelfConversation(chatID, config.UserID) {
		// Para comandos do bot, permitir mais eventos
		botAllowedEvents := map[string]bool{
			"message_created": true,
			"message_updated": true, // Permitir para comandos do bot
		}
		return botAllowedEvents[payload.Event] && payload.MessageType == "outgoing"
	}

	// Para eventos de deleção, permitir processamento independente do MessageType
	if payload.Event == "message_updated" && payload.ContentAttributes.Deleted {
		return true
	}
	
	return true
}

// processTypingEvent processa eventos de typing (conversation_typing_on/off)
// e envia apenas presence para WhatsApp, SEM processar como mensagem
func (w *WebhookProcessor) processTypingEvent(payload WebhookPayload, config *Config) error {
	// Verificar se typing indicator está habilitado na configuração
	if !config.EnableTypingIndicator {
		log.Debug().Msg("Typing indicator desabilitado na configuração")
		return nil
	}
	
	chatID := w.extractChatID(payload)
	if chatID == "" {
		return fmt.Errorf("não foi possível extrair ID do chat para typing event")
	}
	
	// Ignorar typing events para self-conversation
	if w.isSelfConversation(chatID, config.UserID) {
		log.Debug().Msg("Ignorando typing event para self-conversation")
		return nil
	}
	
	// Determinar o tipo de presence baseado no evento
	var presence string
	switch payload.Event {
	case "conversation_typing_on":
		presence = "composing"
	case "conversation_typing_off":
		presence = "paused"
	default:
		return fmt.Errorf("evento de typing não reconhecido: %s", payload.Event)
	}
	
	log.Info().
		Str("chat_id", chatID).
		Str("event", payload.Event).
		Str("presence", presence).
		Int("conversation", payload.Conversation.ID).
		Str("user_id", config.UserID).
		Msg("Processando evento de typing")
	
	// Chamar API de presence do WhatsApp
	err := w.setWhatsAppPresence(config.UserID, chatID, presence)
	if err != nil {
		log.Error().
			Err(err).
			Str("chat_id", chatID).
			Str("presence", presence).
			Msg("Erro ao definir presence no WhatsApp")
		return err
	}
	
	log.Debug().
		Str("chat_id", chatID).
		Str("presence", presence).
		Msg("Presence definido com sucesso")
	
	return nil
}

// setWhatsAppPresence define o status de presence (composing/paused) no WhatsApp
func (w *WebhookProcessor) setWhatsAppPresence(userID, chatID, presence string) error {
	// Obter cliente do WhatsApp
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		// Detectar desconexão via webhook error
		if GlobalReconnectionManager != nil {
			go GlobalReconnectionManager.EnsureMonitoring(userID)
		}
		return fmt.Errorf("cliente WhatsApp não encontrado para user_id: %s", userID)
	}
	
	// Converter chatID para JID
	jid, err := w.parseJID(chatID)
	if err != nil {
		return fmt.Errorf("erro ao converter chatID para JID: %w", err)
	}
	
	// Definir presence no WhatsApp
	err = client.SendChatPresence(jid, types.ChatPresence(presence), types.ChatPresenceMediaText)
	if err != nil {
		return fmt.Errorf("erro ao enviar presence para WhatsApp: %w", err)
	}
	
	log.Debug().
		Str("user_id", userID).
		Str("chat_id", chatID).
		Str("jid", jid.String()).
		Str("presence", presence).
		Msg("Presence enviado com sucesso para WhatsApp")
	
	return nil
}

// saveOutgoingMessage salva mensagem enviada do Chatwoot → WhatsApp no banco local
func (w *WebhookProcessor) saveOutgoingMessage(messageID, content, userID, chatID, messageType string, chatwootMessageID *int) error {
	// Construir JID completo se necessário
	fullChatJID := chatID
	if !strings.Contains(chatID, "@") {
		if len(chatID) > 15 {
			// Grupo
			fullChatJID = chatID + "@g.us"
		} else {
			// Individual
			fullChatJID = chatID + "@s.whatsapp.net"
		}
	}
	
	// Criar MessageRecord para mensagem enviada
	msg := MessageRecord{
		ID:                 messageID,
		UserID:             userID,
		Content:            content,
		SenderName:         "Agente", // Nome padrão para mensagens do Chatwoot
		MessageType:        messageType,
		ChatwootMessageID:  chatwootMessageID, // ✅ Agora vinculado ao Chatwoot!
		FromMe:             true, // Mensagem enviada pelo bot
		ChatJID:            fullChatJID,
	}
	
	err := SaveMessage(w.db, msg)
	if err != nil {
		return fmt.Errorf("failed to save outgoing message: %w", err)
	}
	
	log.Info().
		Str("message_id", messageID).
		Str("user_id", userID).
		Str("chat_jid", fullChatJID).
		Str("content", content).
		Interface("chatwoot_message_id", chatwootMessageID).
		Bool("has_chatwoot_id", chatwootMessageID != nil).
		Msg("💾 Outgoing message saved to database with Chatwoot ID link")
	
	return nil
}

// handleHelpCommand processa comando #help
func (w *WebhookProcessor) handleHelpCommand(payload WebhookPayload, config *Config) error {
	helpMessage := "🤖 **Comandos Disponíveis** 🤖\n\n" +
		"**Reconexão:**\n" +
		"• `#qrcode` ou `#qr` - Gerar QR code para reconectar\n" +
		"• `#status` - Verificar status da conexão\n\n" +
		"**Gerenciamento:**\n" +
		"• `#clearcache` ou `#limpar` - Limpar cache do sistema\n" +
		"• `#disconnect` ou `#desconectar` - Desconectar WhatsApp\n" +
		"• `#init` ou `#iniciar` - Inicializar conexão\n\n" +
		"**Ajuda:**\n" +
		"• `#help` ou `#ajuda` - Mostrar esta lista de comandos\n\n" +
		"💡 **Dica:** Todos os comandos devem começar com `#` (não `/`)\n\n" +
		"_Sistema de comandos ativo_"
	
	return w.sendPrivateMessage(payload.Conversation.ID, helpMessage, config)
}

// handleUnknownCommand trata comandos não reconhecidos
func (w *WebhookProcessor) handleUnknownCommand(payload WebhookPayload, config *Config, command string) error {
	unknownMessage := fmt.Sprintf(
		"❓ **Comando não reconhecido:** `#%s`\n\n" +
		"Digite `#help` para ver todos os comandos disponíveis.\n\n" +
		"**Comandos principais:**\n" +
		"• `#qrcode` - Gerar QR para reconectar\n" +
		"• `#status` - Status da conexão\n" +
		"• `#help` - Lista completa de comandos",
		command,
	)
	
	return w.sendPrivateMessage(payload.Conversation.ID, unknownMessage, config)
}

// handleInitCommand processa comando #init
func (w *WebhookProcessor) handleInitCommand(payload WebhookPayload, config *Config) error {
	// TODO: Implementar lógica de inicialização
	message := "🚀 **Comando de Inicialização**\n\n" +
		"Funcionalidade em desenvolvimento.\n\n" +
		"Use `#qrcode` para reconectar ou `#status` para verificar a conexão."
	
	return w.sendPrivateMessage(payload.Conversation.ID, message, config)
}

// handleClearCacheCommand processa comando #clearcache
func (w *WebhookProcessor) handleClearCacheCommand(payload WebhookPayload, config *Config) error {
	// TODO: Implementar limpeza de cache
	message := "🧹 **Limpeza de Cache**\n\n" +
		"Funcionalidade em desenvolvimento.\n\n" +
		"Cache será limpo automaticamente quando necessário."
	
	return w.sendPrivateMessage(payload.Conversation.ID, message, config)
}

// handleDisconnectCommand processa comando #disconnect
func (w *WebhookProcessor) handleDisconnectCommand(payload WebhookPayload, config *Config) error {
	// TODO: Implementar desconexão forçada
	message := "🔌 **Desconexão do WhatsApp**\n\n" +
		"Funcionalidade em desenvolvimento.\n\n" +
		"Para verificar o status atual, use `#status`."
	
	return w.sendPrivateMessage(payload.Conversation.ID, message, config)
}

// handleQRCodeCommand processa comando #qrcode usando sistema existente
func (w *WebhookProcessor) handleQRCodeCommand(payload WebhookPayload, config *Config) error {
	log.Info().Str("userID", config.UserID).Int("conversationID", payload.Conversation.ID).Msg("🔥 QR Code generation requested via existing system")
	
	// Usar o sistema existente de QR code com tentativas
	if GlobalReconnectionManager != nil {
		return GlobalReconnectionManager.HandleQRCodeRequest(config.UserID, payload.Conversation.ID, config)
	}
	
	// Fallback caso GlobalReconnectionManager não esteja inicializado
	log.Warn().Str("userID", config.UserID).Msg("ReconnectionManager not initialized - using fallback")
	return w.sendPrivateMessage(payload.Conversation.ID, 
		"❌ **Erro ao conectar, por favor, contate o suporte para mais instruções.**", config)
}

// handleStatusCommand processa comando /status
func (w *WebhookProcessor) handleStatusCommand(payload WebhookPayload, config *Config) error {
	log.Info().Str("userID", config.UserID).Msg("Status command requested")
	
	// Verificar status da conexão WhatsApp
	var statusMessage string
	if GlobalClientGetter == nil {
		statusMessage = "❌ **Sistema Indisponível**\n\nClientManager não inicializado"
	} else {
		client := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
		if client == nil {
			statusMessage = "❌ **WhatsApp Desconectado**\n\nCliente não encontrado"
		} else if !client.IsConnected() {
			statusMessage = "⚠️ **WhatsApp Reconectando...**\n\nTentativa em andamento"
		} else {
			// Status detalhado
			statusMessage = fmt.Sprintf(
				"✅ **WhatsApp Conectado**\n\n"+
				"📊 **Status:**\n"+
				"• Conexão: Ativa\n"+
				"• Chatwoot: %s\n"+
				"• Inbox: %s\n"+
				"• Usuário: %s\n\n"+
				"⏰ Verificado: %s",
				config.URL,
				config.NameInbox,
				config.UserID,
				time.Now().Format("15:04:05"),
			)
		}
	}
	
	return w.sendPrivateMessage(payload.Conversation.ID, statusMessage, config)
}

// sendQRCodeMessage envia QR code como mensagem no Chatwoot
func (w *WebhookProcessor) sendQRCodeMessage(conversationID int, qrCodeData string, config *Config) error {
	client := NewClient(*config)
	
	// Extrair apenas o base64 do data URL se necessário
	if strings.Contains(qrCodeData, "data:image/png;base64,") {
		qrCodeData = strings.TrimPrefix(qrCodeData, "data:image/png;base64,")
	}
	
	// Criar attachment para o QR code
	attachmentData := map[string]interface{}{
		"content":      "📱 **QR Code para Conexão**\n\n**Instruções:**\n1️⃣ Abra o WhatsApp no seu celular\n2️⃣ Toque em ⋮ (menu) > Aparelhos conectados\n3️⃣ Toque em 'Conectar um aparelho'\n4️⃣ Escaneie este código\n\n⏰ **Válido por alguns minutos**",
		"message_type": "incoming",
		"private":      true,
		"attachments": []map[string]interface{}{
			{
				"data_url":  fmt.Sprintf("data:image/png;base64,%s", qrCodeData),
				"file_type": "image",
			},
		},
	}
	
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", client.AccountID, conversationID)
	_, err := client.makeRequest("POST", endpoint, attachmentData)
	
	if err != nil {
		return fmt.Errorf("failed to send QR code message: %w", err)
	}
	
	log.Info().
		Str("userID", config.UserID).
		Int("conversationID", conversationID).
		Msg("📱 QR Code sent successfully")
	
	return nil
}

// sendPrivateMessage envia mensagem privada no Chatwoot
func (w *WebhookProcessor) sendPrivateMessage(conversationID int, message string, config *Config) error {
	client := NewClient(*config)
	
	messageData := map[string]interface{}{
		"content":     message,
		"message_type": "incoming",
		"private":     true,
	}
	
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", client.AccountID, conversationID)
	_, err := client.makeRequest("POST", endpoint, messageData)
	
	if err != nil {
		return fmt.Errorf("failed to send private message: %w", err)
	}
	
	log.Debug().
		Str("userID", config.UserID).
		Int("conversationID", conversationID).
		Msg("💬 Private message sent successfully")
	
	return nil
}
