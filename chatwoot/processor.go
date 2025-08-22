package chatwoot

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// ProcessEvent processa eventos do WhatsApp para Chatwoot (função exportada)
func ProcessEvent(userID string, db interface{}, rawEvt interface{}, postmap map[string]interface{}) {
	// Esta é a função que será chamada do main
	processChatwootEvent(userID, db, rawEvt, postmap)
}

// processChatwootEvent processa eventos para Chatwoot
func processChatwootEvent(userID string, db interface{}, rawEvt interface{}, postmap map[string]interface{}) {
	// Converter db para o tipo correto
	sqlxDB, ok := db.(*sqlx.DB)
	if !ok {
		log.Error().Msg("Invalid database type for Chatwoot processor")
		return
	}

	// 1. Verificar se Chatwoot está habilitado para este usuário
	config, err := GetConfigByUserID(sqlxDB, userID)
	if err != nil || config == nil || !config.Enabled {
		// Chatwoot não configurado ou desabilitado - retornar silenciosamente
		return
	}

	// 2. Criar cliente Chatwoot
	client := NewClient(*config)

	// 3. Processar evento baseado no tipo
	switch evt := rawEvt.(type) {
	case *events.Message:
		err = processMessageEvent(client, config, evt)
		if err != nil {
			log.Error().Err(err).
				Str("userID", userID).
				Str("messageID", evt.Info.ID).
				Msg("Failed to process message event for Chatwoot")
		}
	case *events.Receipt:
		err = processReceiptEvent(client, config, evt)
		if err != nil {
			log.Error().Err(err).
				Str("userID", userID).
				Strs("messageIDs", evt.MessageIDs).
				Msg("Failed to process receipt event for Chatwoot")
		}
	case *events.Presence:
		err = processPresenceEvent(client, config, evt)
		if err != nil {
			log.Error().Err(err).
				Str("userID", userID).
				Str("from", evt.From.String()).
				Msg("Failed to process presence event for Chatwoot")
		}
	}
}

// processMessageEvent processa mensagens do WhatsApp → Chatwoot
func processMessageEvent(client *Client, config *Config, evt *events.Message) error {
	// 1. Extrair dados da mensagem
	phone := evt.Info.Sender.User
	if phone == "" {
		phone = evt.Info.Chat.User // Para grupos
	}

	// 2. Verificar se deve ignorar (grupos, etc)
	if shouldIgnoreMessage(config, evt) {
		log.Debug().
			Str("phone", phone).
			Str("messageID", evt.Info.ID).
			Msg("Ignoring message for Chatwoot (filtered)")
		return nil
	}

	// 3. Encontrar ou criar contato
	contact, err := findOrCreateContact(client, phone, evt.Info.PushName)
	if err != nil {
		return fmt.Errorf("failed to find/create contact: %w", err)
	}
	if contact == nil {
		return fmt.Errorf("contact is nil for phone %s", phone)
	}

	// 4. Encontrar ou criar conversa
	conversation, err := findOrCreateConversation(client, contact.ID, config)
	if err != nil {
		return fmt.Errorf("failed to find/create conversation: %w", err)
	}
	if conversation == nil {
		return fmt.Errorf("conversation is nil for contact %d", contact.ID)
	}

	// 5. Processar conteúdo da mensagem
	content := extractMessageContent(evt.Message)
	hasAttach := hasAttachment(evt.Message)

	if content == "" && !hasAttach {
		log.Debug().
			Str("messageID", evt.Info.ID).
			Msg("Skipping message with no content or attachments")
		return nil
	}

	// 6. Enviar para Chatwoot
	// Por enquanto, processar apenas mensagens de texto
	// TODO: Implementar processamento de mídias
	if content != "" {
		return sendTextMessage(client, conversation.ID, content, evt.Info.ID)
	} else if hasAttach {
		// Para anexos, enviar apenas uma mensagem indicativa por enquanto
		return sendTextMessage(client, conversation.ID, "[Arquivo recebido]", evt.Info.ID)
	}

	return nil
}

// processReceiptEvent processa confirmações de leitura
func processReceiptEvent(client *Client, config *Config, evt *events.Receipt) error {
	if evt.Type != types.ReceiptTypeRead {
		return nil // Só processar read receipts
	}

	// 1. Buscar conversa baseada no chat
	phone := evt.Chat.User
	contact, err := findContactByPhone(client, phone)
	if err != nil || contact == nil {
		log.Debug().
			Str("phone", phone).
			Msg("Contact not found for read receipt")
		return nil // Não é erro crítico
	}

	// 2. Marcar mensagens como lidas no Chatwoot
	// (Chatwoot não tem API específica, mas podemos atualizar última atividade)
	return updateLastActivity(client, contact.ID)
}

// processPresenceEvent processa status de presença
func processPresenceEvent(client *Client, config *Config, evt *events.Presence) error {
	phone := evt.From.User

	// Atualizar cache local com status de presença
	// Chatwoot não tem API específica para presença, mas podemos usar para analytics
	updatePresenceCache(phone, !evt.Unavailable)

	log.Debug().
		Str("phone", phone).
		Bool("online", !evt.Unavailable).
		Msg("Updated presence cache for Chatwoot")

	return nil
}

// shouldIgnoreMessage verifica se deve ignorar a mensagem
func shouldIgnoreMessage(config *Config, evt *events.Message) bool {
	// 1. Verificar se é grupo e grupos estão desabilitados
	if evt.Info.Chat.Server == "g.us" && isGroupIgnored(config) {
		return true
	}

	// 2. Verificar lista de JIDs ignorados
	ignoreList := parseIgnoreJids(config.IgnoreJids)
	for _, jid := range ignoreList {
		if evt.Info.Chat.String() == jid {
			return true
		}
	}

	// 3. Ignorar mensagens próprias (IsFromMe)
	if evt.Info.IsFromMe {
		return true
	}

	return false
}

// findOrCreateContact encontra ou cria contato no Chatwoot
func findOrCreateContact(client *Client, phone, name string) (*Contact, error) {
	// 1. Verificar cache
	if contact, found := GlobalCache.GetContact(phone); found {
		return contact, nil
	}

	// 2. Buscar no Chatwoot
	contact, err := client.FindContact(phone)
	if err == nil && contact != nil {
		GlobalCache.SetContact(phone, contact)
		return contact, nil
	}

	// 3. Criar novo contato
	if name == "" {
		name = phone
	}

	inboxID, err := getInboxID(client)
	if err != nil {
		return nil, fmt.Errorf("failed to get inbox ID: %w", err)
	}

	contact, err = client.CreateContact(phone, name, "", inboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to create contact: %w", err)
	}

	GlobalCache.SetContact(phone, contact)
	log.Info().
		Str("phone", phone).
		Str("name", name).
		Int("contactID", contact.ID).
		Msg("Created new contact in Chatwoot")

	return contact, nil
}

// findOrCreateConversation encontra ou cria conversa no Chatwoot
func findOrCreateConversation(client *Client, contactID int, config *Config) (*Conversation, error) {
	inboxID, err := getInboxID(client)
	if err != nil {
		return nil, fmt.Errorf("failed to get inbox ID: %w", err)
	}

	// 1. Verificar cache
	if conv, found := GlobalCache.GetConversation(contactID, inboxID); found {
		return conv, nil
	}

	// 2. Buscar no Chatwoot
	conversation, err := client.FindConversation(contactID, inboxID)
	if err == nil && conversation != nil {
		GlobalCache.SetConversation(contactID, inboxID, conversation)
		return conversation, nil
	}

	// 3. Criar nova conversa
	conversation, err = client.CreateConversation(contactID, inboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	GlobalCache.SetConversation(contactID, inboxID, conversation)
	log.Info().
		Int("contactID", contactID).
		Int("conversationID", conversation.ID).
		Msg("Created new conversation in Chatwoot")

	return conversation, nil
}

// extractMessageContent extrai texto das mensagens
func extractMessageContent(msg *waE2E.Message) string {
	if msg.Conversation != nil {
		return *msg.Conversation
	}
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		return *msg.ExtendedTextMessage.Text
	}
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
		return *msg.ImageMessage.Caption
	}
	if msg.VideoMessage != nil && msg.VideoMessage.Caption != nil {
		return *msg.VideoMessage.Caption
	}
	if msg.DocumentMessage != nil && msg.DocumentMessage.Caption != nil {
		return *msg.DocumentMessage.Caption
	}
	return ""
}

// hasAttachment verifica se a mensagem tem anexo
func hasAttachment(msg *waE2E.Message) bool {
	return msg.ImageMessage != nil ||
		msg.VideoMessage != nil ||
		msg.AudioMessage != nil ||
		msg.DocumentMessage != nil
}

// sendTextMessage envia mensagem de texto para Chatwoot
func sendTextMessage(client *Client, conversationID int, content, sourceID string) error {
	_, err := client.SendMessage(conversationID, content, 0, sourceID) // 0 = incoming
	if err != nil {
		return fmt.Errorf("failed to send text message: %w", err)
	}

	log.Info().
		Int("conversationID", conversationID).
		Str("sourceID", sourceID).
		Msg("Sent text message to Chatwoot")

	return nil
}

// TODO: Implementar processamento de anexos em versão futura
// Por enquanto, focamos apenas em mensagens de texto

// Funções auxiliares

// findContactByPhone busca contato por telefone
func findContactByPhone(client *Client, phone string) (*Contact, error) {
	// Verificar cache primeiro
	if contact, found := GlobalCache.GetContact(phone); found {
		return contact, nil
	}

	// Buscar no Chatwoot
	contact, err := client.FindContact(phone)
	if err != nil {
		return nil, err
	}

	if contact != nil {
		GlobalCache.SetContact(phone, contact)
	}

	return contact, nil
}

// updateLastActivity atualiza última atividade do contato
func updateLastActivity(client *Client, contactID int) error {
	// Chatwoot não tem API específica para read receipts
	// Mas podemos implementar se necessário no futuro
	log.Debug().Int("contactID", contactID).Msg("Read receipt processed")
	return nil
}

// updatePresenceCache atualiza cache de presença
func updatePresenceCache(phone string, online bool) {
	// Cache simples de presença com TTL de 5 minutos
	// Por enquanto, apenas log - TODO: implementar cache de presença se necessário
	status := "offline"
	if online {
		status = "online"
	}
	log.Debug().Str("phone", phone).Str("status", status).Msg("Presence updated")
}

// getInboxID obtém ID do inbox
func getInboxID(client *Client) (int, error) {
	// Por enquanto, retornar o primeiro inbox
	// TODO: Implementar lógica para selecionar inbox correto
	inboxes, err := client.ListInboxes()
	if err != nil {
		return 0, err
	}

	if len(inboxes) == 0 {
		return 0, fmt.Errorf("no inboxes found")
	}

	return inboxes[0].ID, nil
}

// isGroupIgnored verifica se grupos devem ser ignorados
func isGroupIgnored(config *Config) bool {
	// Por padrão, ignorar grupos até ser configurado
	// TODO: Adicionar configuração específica para grupos
	return true
}

// parseIgnoreJids parseia lista de JIDs a ignorar
func parseIgnoreJids(ignoreJidsJSON string) []string {
	// Usar a função já implementada no models.go
	return ParseIgnoreJids(ignoreJidsJSON)
}