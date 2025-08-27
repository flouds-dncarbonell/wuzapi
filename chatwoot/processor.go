package chatwoot

import (
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Constantes para controle anti-loop
const (
	// Margem de tempo para comparação de timestamps (evita race conditions)
	TIMESTAMP_MARGIN = 2 * time.Second
	
	// Idade máxima de mensagem para ser processada (evita reprocessamento de mensagens antigas)
	MAX_MESSAGE_AGE = 5 * time.Minute
	
	// Idade máxima estendida para mensagens com mídia
	MAX_MESSAGE_AGE_EXTENDED = 15 * time.Minute
)

// shouldSkipChatwootProcessing verifica se uma mensagem deve ser ignorada para evitar loop
func shouldSkipChatwootProcessing(evt *events.Message, userID string, db *sqlx.DB) bool {
	messageID := evt.Info.ID
	messageTimestamp := evt.Info.Timestamp
	
	// 1. Verificar se mensagem já existe no banco
	existing, err := FindMessageByStanzaID(db, messageID, userID)
	if err != nil {
		// Em caso de erro na consulta, processar por segurança
		log.Warn().
			Err(err).
			Str("message_id", messageID).
			Str("user_id", userID).
			Msg("⚠️ Erro ao verificar mensagem existente, processando por segurança")
		return false
	}
	
	// 2. Se mensagem existe e tem chatwoot_message_id preenchido
	if existing != nil && existing.ChatwootMessageID != nil {
		log.Debug().
			Str("message_id", messageID).
			Time("message_timestamp", messageTimestamp).
			Time("updated_at", existing.UpdatedAt).
			Int("chatwoot_id", *existing.ChatwootMessageID).
			Msg("⏭️ Skipping: mensagem já processada (tem chatwoot_message_id)")
		return true
	}
	
	// 3. Verificar idade da mensagem (evitar reprocessar mensagens muito antigas)
	messageAge := time.Since(messageTimestamp)
	maxAge := getMaxMessageAge(evt)
	
	if messageAge > maxAge {
		log.Debug().
			Str("message_id", messageID).
			Dur("message_age", messageAge).
			Dur("max_age", maxAge).
			Msg("⏭️ Skipping: mensagem muito antiga para processar")
		return true
	}
	
	// 4. Mensagem deve ser processada
	var existingChatwootID interface{}
	if existing != nil {
		existingChatwootID = existing.ChatwootMessageID
	}
	
	log.Debug().
		Str("message_id", messageID).
		Dur("message_age", messageAge).
		Bool("has_existing", existing != nil).
		Interface("existing_chatwoot_id", existingChatwootID).
		Msg("✅ Processando mensagem para Chatwoot")
	return false
}

// getMaxMessageAge retorna a idade máxima baseada no tipo de mensagem
func getMaxMessageAge(evt *events.Message) time.Duration {
	// Mensagens com mídia podem levar mais tempo para processar
	if evt.Message.GetImageMessage() != nil ||
		evt.Message.GetVideoMessage() != nil ||
		evt.Message.GetAudioMessage() != nil ||
		evt.Message.GetDocumentMessage() != nil {
		return MAX_MESSAGE_AGE_EXTENDED
	}
	return MAX_MESSAGE_AGE
}

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

	// 3. Verificação anti-loop para mensagens
	if msgEvt, ok := rawEvt.(*events.Message); ok {
		if shouldSkipChatwootProcessing(msgEvt, userID, sqlxDB) {
			return // Pular processamento para evitar loop
		}
	}

	// 4. Processar evento baseado no tipo
	switch evt := rawEvt.(type) {
	case *events.Message:
		err = processMessageEvent(client, config, evt, userID, sqlxDB)
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
func processMessageEvent(client *Client, config *Config, evt *events.Message, userID string, db *sqlx.DB) error {
	isGroup := evt.Info.Chat.Server == "g.us"
	
	// 1. Verificar se é uma mensagem deletada (Edit: "7")
	if string(evt.Info.Edit) == "7" && evt.Message.GetProtocolMessage() != nil {
		log.Info().
			Str("message_id", evt.Info.ID).
			Str("chat", evt.Info.Chat.String()).
			Str("edit_type", string(evt.Info.Edit)).
			Msg("🗑️ Detectada deleção WhatsApp → processando para Chatwoot")
		
		return processWhatsAppMessageDeletion(client, config, evt, userID, db)
	}
	
	// 2. Verificar se deve ignorar (grupos, etc)
	if shouldIgnoreMessage(config, evt) {
		return nil
	}

	// 3. Processar contato e conversa baseado no tipo (grupo ou individual)
	if isGroup {
		return processGroupMessage(client, config, evt, userID, db)
	} else {
		return processIndividualMessage(client, config, evt, userID, db)
	}
}

// processIndividualMessage processa mensagem de conversa individual
func processIndividualMessage(client *Client, config *Config, evt *events.Message, userID string, db *sqlx.DB) error {
	// Para conversas individuais, usar Chat sempre
	phone := evt.Info.Chat.User
	
	// 1. Encontrar ou criar contato
	contact, err := findOrCreateContact(client, phone, evt.Info.PushName, config, userID)
	if err != nil {
		return fmt.Errorf("failed to find/create contact: %w", err)
	}
	if contact == nil {
		return fmt.Errorf("contact is nil for phone %s", phone)
	}

	// 2. Encontrar ou criar conversa
	conversation, err := findOrCreateConversation(client, contact.ID, config, phone)
	if err != nil {
		return fmt.Errorf("failed to find/create conversation: %w", err)
	}
	if conversation == nil {
		return fmt.Errorf("conversation is nil for contact %d", contact.ID)
	}

	// 3. Verificar se é uma reação
	if evt.Message.ReactionMessage != nil {
		return processReactionMessage(client, config, evt, conversation.ID, db, userID)
	}

	// 4. Processar conteúdo da mensagem
	content := extractMessageContent(evt.Message)
	hasAttach := hasAttachment(evt.Message)

	if content == "" && !hasAttach {
		log.Debug().
			Str("messageID", evt.Info.ID).
			Msg("Skipping message with no content or attachments")
		return nil
	}

	// 5. Verificar se é uma mensagem citada (reply)
	quotedMessageID := extractQuotedMessageID(evt)
	var replyInfo *ReplyInfo
	if quotedMessageID != "" {
		// Buscar mensagem original no banco para obter ChatwootMessageID
		originalMsg, err := FindMessageByStanzaID(db, quotedMessageID, userID)
		if err != nil {
			log.Warn().Err(err).
				Str("quotedMessageID", quotedMessageID).
				Msg("Failed to find quoted message - continuing without reply info")
		}
		
		replyInfo = &ReplyInfo{
			InReplyToExternalID: quotedMessageID,
		}
		
		// Se encontrou a mensagem original E ela tem ChatwootMessageID, usar
		if originalMsg != nil && originalMsg.ChatwootMessageID != nil {
			replyInfo.InReplyToChatwootID = *originalMsg.ChatwootMessageID
		}
		
		log.Info().
			Str("quotedMessageID", quotedMessageID).
			Str("currentMessageID", evt.Info.ID).
			Bool("found_original", originalMsg != nil).
			Bool("has_chatwoot_id", originalMsg != nil && originalMsg.ChatwootMessageID != nil).
			Msg("📩 Message with quote detected")
	}

	// 6. Enviar para Chatwoot com retry automático
	// Determinar message_type baseado em IsFromMe (como chatwoot-lib)
	messageType := "incoming" // incoming (default)
	if evt.Info.IsFromMe {
		messageType = "outgoing" // outgoing
	}
	
	// Processar mensagens de texto e mídia
	if hasAttach {
		// TODO: Processar mídia com reply
		return processMediaMessage(client, config, evt, userID, conversation.ID, messageType, db)
	} else if content != "" {
		// Processar texto com reply e salvar mensagem
		chatwootMsg, err := sendTextMessageWithReplyRetryAndSave(client, config, phone, conversation.ID, content, evt.Info.ID, messageType, replyInfo, db, userID, evt)
		if err != nil {
			return err
		}
		
		// Salvar mensagem no banco com vínculo do Chatwoot
		if chatwootMsg != nil {
			log.Debug().
				Str("messageID", evt.Info.ID).
				Int("chatwootID", chatwootMsg.ID).
				Str("content", content).
				Msg("🔄 About to save message to database with Chatwoot ID")
			
			err = saveMessageToDB(db, evt, userID, content, chatwootMsg.ID, conversation.ID)
			if err != nil {
				log.Warn().Err(err).
					Str("messageID", evt.Info.ID).
					Int("chatwootID", chatwootMsg.ID).
					Msg("Failed to save message to database - continuing anyway")
			} else {
				log.Info().
					Str("messageID", evt.Info.ID).
					Int("chatwootID", chatwootMsg.ID).
					Msg("✅ Successfully saved message to database with Chatwoot ID")
			}
		} else {
			log.Warn().
				Str("messageID", evt.Info.ID).
				Msg("⚠️ chatwootMsg is nil - cannot save Chatwoot ID to database")
		}
		
		return nil
	}

	return nil
}

// processGroupMessage processa mensagem de grupo como contato único
func processGroupMessage(client *Client, config *Config, evt *events.Message, userID string, db *sqlx.DB) error {
	groupJID := evt.Info.Chat.String()
	
	log.Debug().
		Str("groupJID", groupJID).
		Str("messageID", evt.Info.ID).
		Bool("fromMe", evt.Info.IsFromMe).
		Msg("Processing group message as single contact")

	// Grupos são sempre processados como contato único
	return processGroupAsContact(client, config, evt, userID, db)
}


// processGroupAsContact processa grupo como contato único
func processGroupAsContact(client *Client, config *Config, evt *events.Message, userID string, db *sqlx.DB) error {
	groupPhone := evt.Info.Chat.User
	
	// Buscar nome do grupo
	groupName, err := getGroupName(evt.Info.Chat.String(), userID)
	if err != nil || groupName == "" {
		groupName = fmt.Sprintf("Grupo %s", groupPhone)
	} else {
		groupName = fmt.Sprintf("%s (GROUP)", groupName)
	}
	
	// 1. Encontrar ou criar contato para o grupo
	contact, err := findOrCreateContact(client, groupPhone, groupName, config, userID)
	if err != nil {
		return fmt.Errorf("failed to find/create group contact: %w", err)
	}
	if contact == nil {
		return fmt.Errorf("group contact is nil for phone %s", groupPhone)
	}
	
	// 2. Encontrar ou criar conversa
	conversation, err := findOrCreateConversation(client, contact.ID, config, groupPhone)
	if err != nil {
		return fmt.Errorf("failed to find/create group conversation: %w", err)
	}
	if conversation == nil {
		return fmt.Errorf("group conversation is nil for contact %d", contact.ID)
	}
	
	// 3. Verificar se é uma reação (grupos também podem ter reações)
	if evt.Message.ReactionMessage != nil {
		return processGroupReactionMessage(client, config, evt, conversation.ID, db, userID)
	}

	// 4. Processar conteúdo da mensagem
	content := extractMessageContent(evt.Message)
	hasAttach := hasAttachment(evt.Message)
	
	log.Debug().
		Str("groupJID", evt.Info.Chat.String()).
		Str("senderJID", evt.Info.Sender.String()).
		Str("content", content).
		Int("contentLength", len(content)).
		Bool("hasAttachment", hasAttach).
		Bool("fromMe", evt.Info.IsFromMe).
		Msg("Group message content processing")
	
	if content == "" && !hasAttach {
		log.Debug().
			Str("groupJID", evt.Info.Chat.String()).
			Msg("Skipping group message with no content or attachments")
		return nil
	}
	
	// 5. Verificar se mensagem menciona o bot (para prioridade)
	isMentioned := !evt.Info.IsFromMe && isMessageMentioningBot(evt, userID)
	if isMentioned {
		log.Debug().
			Str("groupJID", evt.Info.Chat.String()).
			Str("messageID", evt.Info.ID).
			Msg("Bot mentioned in group - setting high priority")
		
		// Definir prioridade alta quando mencionado
		err := client.SetConversationPriority(conversation.ID, "high")
		if err != nil {
			log.Warn().
				Err(err).
				Int("conversationID", conversation.ID).
				Msg("Failed to set conversation priority - continuing anyway")
		}
	}

	// 6. Para mensagens de grupo, adicionar informações do remetente apenas quando não for do próprio bot
	if !evt.Info.IsFromMe {
		// Para mensagens de outros participantes, adicionar prefixo com info do remetente
		// Formatar como: +55 (51) 8486-8314 - Daniel Carbonell
		senderPhone := evt.Info.Sender.User
		senderName := evt.Info.PushName
		
		if senderName == "" {
			senderName = "Desconhecido"
		}
		
		// Formatar número de telefone
		formattedPhone := formatPhoneNumber(senderPhone)
		senderInfo := fmt.Sprintf("%s - %s", formattedPhone, senderName)
		
		// Prefixar mensagens de outros participantes com formatação em markdown
		content = fmt.Sprintf("**%s:**\n\n%s", senderInfo, content)
	}
	// Para mensagens do próprio bot (IsFromMe=true), usar conteúdo sem prefixo
	
	// 6.5. Verificar se é uma mensagem citada (reply) - igual ao individual
	quotedMessageID := extractQuotedMessageID(evt)
	var replyInfo *ReplyInfo
	if quotedMessageID != "" {
		// Buscar mensagem original no banco para obter ChatwootMessageID
		originalMsg, err := FindMessageByStanzaID(db, quotedMessageID, userID)
		if err != nil {
			log.Warn().Err(err).
				Str("quotedMessageID", quotedMessageID).
				Msg("Failed to find quoted message - continuing without reply info")
		}
		
		replyInfo = &ReplyInfo{
			InReplyToExternalID: quotedMessageID,
		}
		
		// Se encontrou a mensagem original E ela tem ChatwootMessageID, usar
		if originalMsg != nil && originalMsg.ChatwootMessageID != nil {
			replyInfo.InReplyToChatwootID = *originalMsg.ChatwootMessageID
		}
		
		log.Info().
			Str("quotedMessageID", quotedMessageID).
			Str("currentMessageID", evt.Info.ID).
			Str("groupJID", evt.Info.Chat.String()).
			Bool("found_original", originalMsg != nil).
			Bool("has_chatwoot_id", originalMsg != nil && originalMsg.ChatwootMessageID != nil).
			Msg("📩 Group message with quote detected")
	}
	
	// 7. Enviar para Chatwoot
	messageType := "incoming"
	if evt.Info.IsFromMe {
		messageType = "outgoing"
	}
	
	if hasAttach {
		// TODO: Processar mídia de grupo com reply
		return processGroupMediaMessage(client, config, evt, userID, conversation.ID, messageType, content, db)
	} else if content != "" {
		// Processar texto de grupo com reply e salvar mensagem
		chatwootMsg, err := sendTextMessageWithReplyRetryAndSave(client, config, groupPhone, conversation.ID, content, evt.Info.ID, messageType, replyInfo, db, userID, evt)
		if err != nil {
			return err
		}
		
		// Salvar mensagem no banco com vínculo do Chatwoot
		if chatwootMsg != nil {
			log.Debug().
				Str("messageID", evt.Info.ID).
				Int("chatwootID", chatwootMsg.ID).
				Str("groupJID", evt.Info.Chat.String()).
				Msg("🔄 About to save group message to database with Chatwoot ID")
			
			err = saveMessageToDB(db, evt, userID, content, chatwootMsg.ID, conversation.ID)
			if err != nil {
				log.Warn().Err(err).
					Str("messageID", evt.Info.ID).
					Int("chatwootID", chatwootMsg.ID).
					Msg("Failed to save group message to database - continuing anyway")
			} else {
				log.Info().
					Str("messageID", evt.Info.ID).
					Int("chatwootID", chatwootMsg.ID).
					Str("groupJID", evt.Info.Chat.String()).
					Msg("✅ Successfully saved group message to database with Chatwoot ID")
			}
		} else {
			log.Warn().
				Str("messageID", evt.Info.ID).
				Str("groupJID", evt.Info.Chat.String()).
				Msg("⚠️ chatwootMsg is nil - cannot save Chatwoot ID to database")
		}
		
		return nil
	}
	
	return nil
}

// processReceiptEvent processa confirmações de leitura
func processReceiptEvent(client *Client, config *Config, evt *events.Receipt) error {
	if evt.Type != types.ReceiptTypeRead {
		return nil // Só processar read receipts
	}

	// Processar cada messageID individualmente (como chatwoot-lib)
	for _, messageID := range evt.MessageIDs {
		err := processIndividualReadReceipt(client, config, evt, messageID)
		if err != nil {
			log.Error().Err(err).
				Str("messageID", messageID).
				Str("phone", evt.Chat.User).
				Msg("Failed to process individual read receipt")
		}
	}

	return nil
}

// processIndividualReadReceipt processa um read receipt individual por messageID
func processIndividualReadReceipt(client *Client, config *Config, evt *events.Receipt, messageID string) error {
	phone := evt.Chat.User
	
	log.Debug().
		Str("messageID", messageID).
		Str("phone", phone).
		Msg("Processing individual read receipt")

	// 1. Buscar contato
	contact, err := findContactByPhone(client, phone)
	if err != nil || contact == nil {
		log.Debug().
			Str("phone", phone).
			Str("messageID", messageID).
			Msg("Contact not found for read receipt")
		return nil // Não é erro crítico
	}

	// 2. Encontrar conversa ativa
	inboxID, err := getInboxID(client, config)
	if err != nil {
		log.Error().Err(err).Str("messageID", messageID).Msg("Failed to get inbox ID for read receipt")
		return nil
	}

	conversations, err := client.ListContactConversations(contact.ID)
	if err != nil {
		log.Error().Err(err).
			Int("contactID", contact.ID).
			Str("messageID", messageID).
			Msg("Error listing contact conversations for read receipt")
		return nil
	}

	// Buscar conversa no inbox específico
	var conversationID int
	for _, conv := range conversations {
		if conv.InboxID == inboxID && conv.Status != "resolved" {
			conversationID = conv.ID
			break
		}
	}

	if conversationID == 0 {
		log.Debug().
			Str("phone", phone).
			Int("contactID", contact.ID).
			Str("messageID", messageID).
			Msg("No active conversation found for read receipt")
		return nil
	}

	// 3. Verificar se este messageID específico já foi processado recentemente 
	cacheKey := fmt.Sprintf("read_receipt:%s", messageID)
	if GlobalCache.HasProcessedReadReceipt(cacheKey) {
		log.Debug().
			Str("messageID", messageID).
			Int("conversationID", conversationID).
			Msg("Read receipt already processed for this messageID")
		return nil
	}

	// 4. Buscar source_id do cache do webhook primeiro (mais rápido e confiável)
	sourceID, found := client.getSourceIDFromWebhookCache(conversationID)
	if !found {
		log.Debug().
			Int("conversationID", conversationID).
			Str("messageID", messageID).
			Msg("Source_id not found in webhook cache, trying fallback")
			
		// Fallback: buscar via /contacts/filter (método que funciona)
		sourceID, err = client.getContactSourceID(contact.ID, inboxID)
		if err != nil {
			log.Error().Err(err).
				Int("contactID", contact.ID).
				Int("inboxID", inboxID).
				Str("phone", phone).
				Str("messageID", messageID).
				Msg("All methods failed to get source_id - conversation may not exist properly")
			return fmt.Errorf("unable to get source_id for contact %d in inbox %d", contact.ID, inboxID)
		}
	}
	
	log.Debug().
		Int("contactID", contact.ID).
		Int("inboxID", inboxID).
		Str("sourceID", sourceID).
		Str("messageID", messageID).
		Msg("Successfully obtained source_id for read receipt")
	
	// 5. Atualizar last seen na conversa com endpoint oficial usando source_id correto
	err = client.UpdateLastSeenDirect(conversationID, inboxID, sourceID)
	if err != nil {
		log.Error().Err(err).
			Int("conversationID", conversationID).
			Int("contactID", contact.ID).
			Int("inboxID", inboxID).
			Str("sourceID", sourceID).
			Str("phone", phone).
			Str("messageID", messageID).
			Msg("Failed to update last seen for read receipt with official endpoint")
		return err
	}

	// 6. Marcar este messageID como processado (previne duplicação)
	GlobalCache.MarkReadReceiptProcessed(cacheKey)

	log.Info().
		Str("phone", phone).
		Int("contactID", contact.ID).
		Int("conversationID", conversationID).
		Str("messageID", messageID).
		Msg("Processed read receipt - updated last seen")

	return nil
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
	isGroup := evt.Info.Chat.Server == "g.us"
	
	// 1. Verificar se é grupo e grupos estão desabilitados
	if isGroup && isGroupIgnored(config) {
		log.Debug().
			Str("chatJID", evt.Info.Chat.String()).
			Msg("Ignoring group message - groups disabled")
		return true
	}


	// 2. Verificar lista de JIDs ignorados
	ignoreList := parseIgnoreJids(config.IgnoreJids)
	for _, jid := range ignoreList {
		if evt.Info.Chat.String() == jid {
			log.Debug().
				Str("chatJID", evt.Info.Chat.String()).
				Msg("Ignoring message - JID in ignore list")
			return true
		}
	}

	// 3. Ignorar mensagens de status/broadcast
	if evt.Info.Chat.User == "status" || evt.Info.Chat.String() == "status@broadcast" {
		return true
	}

	return false
}

// findOrCreateContact encontra ou cria contato no Chatwoot
func findOrCreateContact(client *Client, phone, name string, config *Config, userID string) (*Contact, error) {
	// 1. Verificar cache
	if contact, found := GlobalCache.GetContact(phone); found {
		// 1.1. Verificar se deve atualizar avatar do contato existente
		if GlobalCache.ShouldCheckAvatar(phone) {
			log.Debug().
				Str("phone", phone).
				Int("contactID", contact.ID).
				Msg("Checking if contact avatar needs update")
			
			if err := checkAndUpdateContactAvatar(client, contact, phone, userID); err != nil {
				log.Error().Err(err).
					Str("phone", phone).
					Int("contactID", contact.ID).
					Msg("Failed to update contact avatar")
				// Não é erro crítico, continuar com contato existente
			}
			
			// Marcar como verificado independentemente do resultado
			GlobalCache.MarkAvatarChecked(phone)
		}
		
		return contact, nil
	}

	// 2. Buscar no Chatwoot
	contact, err := client.FindContact(phone)
	if err == nil && contact != nil {
		GlobalCache.SetContact(phone, contact)
		
		// 2.1. Verificar avatar para contato encontrado no Chatwoot mas não no cache
		if GlobalCache.ShouldCheckAvatar(phone) {
			log.Debug().
				Str("phone", phone).
				Int("contactID", contact.ID).
				Msg("Checking avatar for contact found in Chatwoot")
			
			if err := checkAndUpdateContactAvatar(client, contact, phone, userID); err != nil {
				log.Error().Err(err).
					Str("phone", phone).
					Int("contactID", contact.ID).
					Msg("Failed to update contact avatar")
			}
			
			GlobalCache.MarkAvatarChecked(phone)
		}
		
		return contact, nil
	}

	// 3. Criar novo contato
	if name == "" {
		name = phone
	}

	inboxID, err := getInboxID(client, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get inbox ID: %w", err)
	}

	// 4. Obter imagem de perfil do WhatsApp
	avatarURL := getWhatsAppProfilePicture(phone, userID)

	contact, err = client.CreateContact(phone, name, avatarURL, inboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to create contact: %w", err)
	}

	GlobalCache.SetContact(phone, contact)
	GlobalCache.MarkAvatarChecked(phone) // Marcar como verificado
	
	log.Info().
		Str("phone", phone).
		Str("name", name).
		Str("avatar_url", avatarURL).
		Int("contactID", contact.ID).
		Msg("Created new contact in Chatwoot")

	return contact, nil
}

// findOrCreateConversation encontra ou cria conversa no Chatwoot (seguindo lógica chatwoot-lib)
func findOrCreateConversation(client *Client, contactID int, config *Config, phone string) (*Conversation, error) {
	inboxID, err := getInboxID(client, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get inbox ID: %w", err)
	}

	// 1. Verificar cache por phone (seguindo chatwoot-lib)
	cacheKey := fmt.Sprintf("conversation:%s:%d", phone, inboxID)
	if conv, found := GlobalCache.GetConversationByKey(cacheKey); found {
		log.Debug().
			Str("phone", phone).
			Int("conversationID", conv.ID).
			Msg("Found conversation in cache")
		return conv, nil
	}

	// 2. Buscar conversas existentes do contato (como chatwoot-lib)
	conversations, err := client.ListContactConversations(contactID)
	if err != nil {
		log.Error().Err(err).Int("contactID", contactID).Msg("Error listing contact conversations")
	} else {
		// Buscar conversa no inbox específico
		for _, conv := range conversations {
			if conv.InboxID == inboxID {
				// Lógica reopenConversation (como chatwoot-lib)
				if config.ReopenConversation {
					// reopenConversation=true: usa qualquer conversa (mesmo resolved)
					log.Info().
						Int("conversationID", conv.ID).
						Int("contactID", contactID).
						Str("status", conv.Status).
						Bool("reopen_mode", true).
						Msg("Found conversation in reopen mode")
					
					// Se conversation_pending=true e status != open, mudar para pending
					if config.ConversationPending && conv.Status != "open" {
						err = client.ToggleConversationStatus(conv.ID, "pending")
						if err != nil {
							log.Error().Err(err).Int("conversationID", conv.ID).Msg("Error setting conversation to pending")
						}
					}
					
					GlobalCache.SetConversationByKey(cacheKey, &conv)
					return &conv, nil
				} else {
					// reopenConversation=false: só conversas ativas (não resolved)
					if conv.Status != "resolved" {
						log.Info().
							Int("conversationID", conv.ID).
							Int("contactID", contactID).
							Str("status", conv.Status).
							Bool("reopen_mode", false).
							Msg("Found active conversation")
						
						GlobalCache.SetConversationByKey(cacheKey, &conv)
						return &conv, nil
					}
				}
			}
		}
	}

	// 3. Criar nova conversa (como chatwoot-lib, SEM source_id)
	conversation, err := client.CreateConversation(contactID, inboxID)
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
	// Debug: log dos tipos de mensagem disponíveis
	log.Debug().
		Bool("has_conversation", msg.Conversation != nil).
		Bool("has_extended_text", msg.ExtendedTextMessage != nil).
		Bool("has_image", msg.ImageMessage != nil).
		Bool("has_video", msg.VideoMessage != nil).
		Bool("has_document", msg.DocumentMessage != nil).
		Bool("has_sticker", msg.StickerMessage != nil).
		Bool("has_reaction", msg.ReactionMessage != nil).
		Msg("Extracting message content - available message types")

	if msg.Conversation != nil {
		content := *msg.Conversation
		log.Debug().
			Str("content", content).
			Int("length", len(content)).
			Msg("Extracted content from Conversation field")
		return content
	}
	
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		content := *msg.ExtendedTextMessage.Text
		log.Debug().
			Str("content", content).
			Int("length", len(content)).
			Msg("Extracted content from ExtendedTextMessage field")
		return content
	}
	
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
		content := *msg.ImageMessage.Caption
		log.Debug().
			Str("content", content).
			Int("length", len(content)).
			Msg("Extracted content from ImageMessage caption")
		return content
	}
	
	if msg.VideoMessage != nil && msg.VideoMessage.Caption != nil {
		content := *msg.VideoMessage.Caption
		log.Debug().
			Str("content", content).
			Int("length", len(content)).
			Msg("Extracted content from VideoMessage caption")
		return content
	}
	
	if msg.DocumentMessage != nil && msg.DocumentMessage.Caption != nil {
		content := *msg.DocumentMessage.Caption
		log.Debug().
			Str("content", content).
			Int("length", len(content)).
			Msg("Extracted content from DocumentMessage caption")
		return content
	}

	// Tratamento específico para reações
	if msg.ReactionMessage != nil {
		// Extrair emoji da reação
		reactionEmoji := ""
		if msg.ReactionMessage.Text != nil {
			reactionEmoji = *msg.ReactionMessage.Text
		}
		
		// Se não há emoji (remoção de reação), usar texto padrão
		if reactionEmoji == "" {
			reactionEmoji = "❌" // Indicar remoção de reação
		}
		
		log.Debug().
			Str("reaction", reactionEmoji).
			Msg("Extracted reaction emoji")
		return reactionEmoji
	}
	
	// Para stickers, não retornar texto - será tratado como mídia pura
	if msg.StickerMessage != nil {
		log.Debug().Msg("Sticker message detected - no text content")
		return "" // Sem texto - apenas mídia
	}
	
	log.Debug().Msg("No text content found in message")
	return ""
}

// hasAttachment verifica se a mensagem tem anexo
func hasAttachment(msg *waE2E.Message) bool {
	return msg.ImageMessage != nil ||
		msg.VideoMessage != nil ||
		msg.AudioMessage != nil ||
		msg.DocumentMessage != nil ||
		msg.StickerMessage != nil
}

// extractQuotedMessageID extrai o StanzaID de uma mensagem citada do ContextInfo
func extractQuotedMessageID(evt *events.Message) string {
	msg := evt.Message
	
	// Verificar ExtendedTextMessage
	if msg.ExtendedTextMessage != nil && 
	   msg.ExtendedTextMessage.ContextInfo != nil &&
	   msg.ExtendedTextMessage.ContextInfo.StanzaID != nil {
		return *msg.ExtendedTextMessage.ContextInfo.StanzaID
	}
	
	// Verificar ImageMessage
	if msg.ImageMessage != nil &&
	   msg.ImageMessage.ContextInfo != nil &&
	   msg.ImageMessage.ContextInfo.StanzaID != nil {
		return *msg.ImageMessage.ContextInfo.StanzaID
	}
	
	// Verificar VideoMessage
	if msg.VideoMessage != nil &&
	   msg.VideoMessage.ContextInfo != nil &&
	   msg.VideoMessage.ContextInfo.StanzaID != nil {
		return *msg.VideoMessage.ContextInfo.StanzaID
	}
	
	// Verificar DocumentMessage
	if msg.DocumentMessage != nil &&
	   msg.DocumentMessage.ContextInfo != nil &&
	   msg.DocumentMessage.ContextInfo.StanzaID != nil {
		return *msg.DocumentMessage.ContextInfo.StanzaID
	}
	
	// Verificar AudioMessage
	if msg.AudioMessage != nil &&
	   msg.AudioMessage.ContextInfo != nil &&
	   msg.AudioMessage.ContextInfo.StanzaID != nil {
		return *msg.AudioMessage.ContextInfo.StanzaID
	}
	
	return ""
}

// saveMessageToDB salva mensagem no banco com vínculo Chatwoot
func saveMessageToDB(db *sqlx.DB, evt *events.Message, userID string, content string, chatwootMessageID int, chatwootConversationID int) error {
	log.Debug().
		Str("whatsapp_id", evt.Info.ID).
		Str("user_id", userID).
		Int("chatwoot_id", chatwootMessageID).
		Int("chatwoot_conversation_id", chatwootConversationID).
		Str("content", content).
		Msg("📥 saveMessageToDB called with parameters")
	// Determinar tipo da mensagem
	messageType := "text"
	if evt.Message.ImageMessage != nil {
		messageType = "image"
	} else if evt.Message.VideoMessage != nil {
		messageType = "video"
	} else if evt.Message.AudioMessage != nil {
		messageType = "audio"
	} else if evt.Message.DocumentMessage != nil {
		messageType = "document"
	}
	
	// Determinar nome do remetente
	senderName := evt.Info.PushName
	if senderName == "" {
		if evt.Info.IsFromMe {
			senderName = "Eu"
		} else {
			senderName = evt.Info.Chat.User // Fallback para número
		}
	}
	
	msg := MessageRecord{
		ID:                     evt.Info.ID,
		UserID:                 userID,
		Content:                content,
		SenderName:             senderName,
		MessageType:            messageType,
		ChatwootMessageID:      &chatwootMessageID,
		ChatwootConversationID: &chatwootConversationID,
		FromMe:                 evt.Info.IsFromMe,
		ChatJID:                evt.Info.Chat.String(),
	}
	
	return SaveMessage(db, msg)
}

// sendTextMessage envia mensagem de texto para Chatwoot
func sendTextMessage(client *Client, conversationID int, content, sourceID string, messageType string) error {
	return sendTextMessageWithReply(client, conversationID, content, sourceID, messageType, nil)
}

// sendTextMessageWithReply envia mensagem de texto com suporte a reply
func sendTextMessageWithReply(client *Client, conversationID int, content, sourceID string, messageType string, replyInfo *ReplyInfo) error {
	_, err := client.SendMessageWithReply(conversationID, content, messageType, sourceID, replyInfo)
	if err != nil {
		return fmt.Errorf("failed to send text message: %w", err)
	}

	log.Info().
		Int("conversationID", conversationID).
		Str("sourceID", sourceID).
		Str("messageType", messageType).
		Bool("hasReply", replyInfo != nil).
		Msg("Sent text message to Chatwoot")

	return nil
}

// sendTextMessageWithReplyRetryAndSave envia mensagem com reply, retry e retorna Chatwoot Message
func sendTextMessageWithReplyRetryAndSave(client *Client, config *Config, phone string, conversationID int, content, sourceID string, messageType string, replyInfo *ReplyInfo, db *sqlx.DB, userID string, evt *events.Message) (*Message, error) {
	maxRetries := 2 // 2 retries = 3 tentativas totais
	
	for i := 0; i <= maxRetries; i++ {
		chatwootMsg, err := client.SendMessageWithReply(conversationID, content, messageType, sourceID, replyInfo)
		
		if err == nil {
			// Sucesso - retornar mensagem
			return chatwootMsg, nil
		}
		
		// Verificar se é erro 404 (conversa não existe)
		if strings.Contains(err.Error(), "404") && i < maxRetries {
			log.Warn().
				Err(err).
				Int("attempt", i+1).
				Int("maxRetries", maxRetries).
				Str("phone", phone).
				Int("conversationID", conversationID).
				Msg("Conversation not found (404), recreating and retrying...")
			
			// Limpar cache da conversa inválida
			inboxID, _ := getInboxID(client, config)
			cacheKey := fmt.Sprintf("conversation:%s:%d", phone, inboxID)
			GlobalCache.conversations.Delete(cacheKey)
			
			log.Debug().
				Str("cacheKey", cacheKey).
				Msg("Invalidated conversation cache due to 404")
			
			// Recriar conversa
			contact, contactErr := findOrCreateContact(client, phone, "", config, userID)
			if contactErr != nil {
				log.Error().
					Err(contactErr).
					Str("phone", phone).
					Msg("Failed to recreate contact during retry")
				continue
			}
			
			conversation, convErr := findOrCreateConversation(client, contact.ID, config, phone)
			if convErr != nil {
				log.Error().
					Err(convErr).
					Int("contactID", contact.ID).
					Msg("Failed to recreate conversation during retry")
				continue
			}
			
			// Atualizar conversationID para próxima tentativa
			conversationID = conversation.ID
			continue
		}
		
		// Se não é 404 ou já esgotamos retries, falhar
		if i == maxRetries {
			return nil, fmt.Errorf("failed after %d retries: %w", maxRetries+1, err)
		}
	}
	
	return nil, fmt.Errorf("unexpected end of retry loop")
}

// sendTextMessageWithReplyRetry envia mensagem com reply e retry automático em caso de 404
func sendTextMessageWithReplyRetry(client *Client, config *Config, phone string, conversationID int, content, sourceID string, messageType string, replyInfo *ReplyInfo) error {
	maxRetries := 2 // 2 retries = 3 tentativas totais
	
	for i := 0; i <= maxRetries; i++ {
		_, err := client.SendMessageWithReply(conversationID, content, messageType, sourceID, replyInfo)
		
		if err == nil {
			// Sucesso - retornar imediatamente
			return nil
		}
		
		// Verificar se é erro 404 (conversa não existe)
		if strings.Contains(err.Error(), "404") && i < maxRetries {
			log.Warn().
				Err(err).
				Int("attempt", i+1).
				Int("maxRetries", maxRetries).
				Str("phone", phone).
				Int("conversationID", conversationID).
				Msg("Conversation not found (404), recreating and retrying...")
			
			// Limpar cache da conversa inválida
			inboxID, _ := getInboxID(client, config)
			cacheKey := fmt.Sprintf("conversation:%s:%d", phone, inboxID)
			GlobalCache.conversations.Delete(cacheKey)
			
			log.Debug().
				Str("cacheKey", cacheKey).
				Msg("Invalidated conversation cache due to 404")
			
			// Recriar conversa
			contact, contactErr := findOrCreateContact(client, phone, "", config, config.UserID)
			if contactErr != nil {
				log.Error().
					Err(contactErr).
					Str("phone", phone).
					Msg("Failed to recreate contact during retry")
				continue
			}
			
			conversation, convErr := findOrCreateConversation(client, contact.ID, config, phone)
			if convErr != nil {
				log.Error().
					Err(convErr).
					Int("contactID", contact.ID).
					Msg("Failed to recreate conversation during retry")
				continue
			}
			
			// Atualizar conversationID para próxima tentativa
			conversationID = conversation.ID
			continue
		}
		
		// Se não é 404 ou já esgotamos retries, falhar
		if i == maxRetries {
			return fmt.Errorf("failed after %d retries: %w", maxRetries+1, err)
		}
	}
	
	return fmt.Errorf("unexpected end of retry loop")
}

// sendTextMessageWithRetry envia mensagem com retry automático em caso de 404
func sendTextMessageWithRetry(client *Client, config *Config, phone string, conversationID int, content, sourceID string, messageType string) error {
	return sendTextMessageWithReplyRetry(client, config, phone, conversationID, content, sourceID, messageType, nil)
}

// Legacy function for backward compatibility
func sendTextMessageWithRetryOld(client *Client, config *Config, phone string, conversationID int, content, sourceID string, messageType string) error {
	maxRetries := 2 // 2 retries = 3 tentativas totais
	retryInterval := 5 * time.Second
	
	log.Debug().
		Str("phone", phone).
		Int("conversationID", conversationID).
		Int("maxRetries", maxRetries).
		Str("retryInterval", retryInterval.String()).
		Msg("Starting message send with retry logic")

	for attempt := 0; attempt <= maxRetries; attempt++ {
		log.Debug().
			Int("attempt", attempt+1).
			Int("maxAttempts", maxRetries+1).
			Int("conversationID", conversationID).
			Str("phone", phone).
			Msg("Attempting to send message")

		// Tentar enviar mensagem
		err := sendTextMessage(client, conversationID, content, sourceID, messageType)
		
		if err == nil {
			// Sucesso!
			if attempt > 0 {
				log.Info().
					Int("attempt", attempt+1).
					Str("phone", phone).
					Int("conversationID", conversationID).
					Msg("Message sent successfully after retry")
			}
			return nil
		}

		// Verificar se é erro 404 (conversa não encontrada)
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "Resource could not be found") {
			log.Warn().
				Err(err).
				Int("attempt", attempt+1).
				Int("maxAttempts", maxRetries+1).
				Str("phone", phone).
				Int("conversationID", conversationID).
				Msg("Conversation not found (404) - will clear cache and retry")

			// Se ainda temos tentativas restantes
			if attempt < maxRetries {
				// Limpar cache (conversa + contato)
				invalidateCacheForPhone(phone)
				
				log.Info().
					Str("phone", phone).
					Int("oldConversationID", conversationID).
					Int("retryAttempt", attempt+2).
					Int("maxAttempts", maxRetries+1).
					Msg("Cleared cache, recreating contact and conversation")

				// Aguardar intervalo antes de retry
				log.Debug().
					Str("interval", retryInterval.String()).
					Msg("Waiting before retry")
				time.Sleep(retryInterval)

				// Recriar contato e conversa do zero
				contact, err := findOrCreateContact(client, phone, "", config, "")
				if err != nil {
					log.Error().
						Err(err).
						Str("phone", phone).
						Int("retryAttempt", attempt+2).
						Msg("Failed to recreate contact during retry")
					continue // Tenta próximo retry
				}

				conversation, err := findOrCreateConversation(client, contact.ID, config, phone)
				if err != nil {
					log.Error().
						Err(err).
						Str("phone", phone).
						Int("contactID", contact.ID).
						Int("retryAttempt", attempt+2).
						Msg("Failed to recreate conversation during retry")
					continue // Tenta próximo retry
				}

				// Atualizar conversationID para próxima tentativa
				conversationID = conversation.ID
				
				log.Info().
					Str("phone", phone).
					Int("newConversationID", conversationID).
					Int("contactID", contact.ID).
					Int("retryAttempt", attempt+2).
					Msg("Successfully recreated contact and conversation for retry")
			}
		} else {
			// Erro diferente de 404, não vale a pena fazer retry
			log.Error().
				Err(err).
				Int("attempt", attempt+1).
				Str("phone", phone).
				Int("conversationID", conversationID).
				Msg("Non-404 error occurred, no retry will be attempted")
			return err
		}
	}

	// Se chegou aqui, todas as tentativas falharam
	log.Error().
		Int("totalAttempts", maxRetries+1).
		Str("phone", phone).
		Int("conversationID", conversationID).
		Msg("Failed to send message after all retry attempts")
	
	return fmt.Errorf("failed to send message after %d attempts", maxRetries+1)
}

// invalidateCacheForPhone limpa cache de contato e conversas para um telefone específico
func invalidateCacheForPhone(phone string) {
	log.Info().
		Str("phone", phone).
		Msg("Invalidating cache for phone number")

	// Limpar cache do contato
	GlobalCache.InvalidateContact(phone)
	
	// Limpar cache de verificação de avatar
	GlobalCache.InvalidateAvatarCheck(phone)
	
	// Limpar todas as conversas relacionadas a este telefone
	// Como não temos acesso direto ao inboxID aqui, vamos limpar por padrão chave
	cacheKey := fmt.Sprintf("conversation:%s:", phone) // Prefixo para buscar conversas
	GlobalCache.InvalidateConversationsByPrefix(cacheKey)
	
	log.Debug().
		Str("phone", phone).
		Str("cacheKeyPrefix", cacheKey).
		Msg("Cache invalidated for phone number")
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

// getInboxID obtém ID do inbox baseado na configuração do usuário
func getInboxID(client *Client, config *Config) (int, error) {
	// Se name_inbox estiver configurado, buscar por nome
	if config.NameInbox != "" {
		log.Debug().
			Str("name_inbox", config.NameInbox).
			Msg("Searching inbox by configured name")
			
		inbox, err := client.GetInboxByName(config.NameInbox)
		if err != nil {
			return 0, fmt.Errorf("failed to search inbox by name '%s': %w", config.NameInbox, err)
		}
		
		if inbox != nil {
			log.Info().
				Str("name_inbox", config.NameInbox).
				Int("inbox_id", inbox.ID).
				Msg("Found inbox by configured name")
			return inbox.ID, nil
		}
		
		log.Warn().
			Str("name_inbox", config.NameInbox).
			Msg("Inbox not found by configured name, falling back to first available")
	}
	
	// Fallback: retornar o primeiro inbox disponível
	log.Debug().Msg("Using first available inbox as fallback")
	inboxes, err := client.ListInboxes()
	if err != nil {
		return 0, fmt.Errorf("failed to list inboxes: %w", err)
	}

	if len(inboxes) == 0 {
		return 0, fmt.Errorf("no inboxes found")
	}

	log.Info().
		Int("inbox_id", inboxes[0].ID).
		Str("inbox_name", inboxes[0].Name).
		Msg("Using first available inbox")
	
	return inboxes[0].ID, nil
}

// isGroupIgnored verifica se grupos devem ser ignorados
func isGroupIgnored(config *Config) bool {
	// Usar configuração específica do usuário
	return config.IgnoreGroups
}

// isMessageMentioningBot verifica se a mensagem menciona o bot
// Precisa receber o userID para obter o JID correto da instância
func isMessageMentioningBot(evt *events.Message, userID string) bool {
	// Obter o JID da instância (bot) via GlobalClientGetter
	if GlobalClientGetter == nil {
		log.Debug().Msg("GlobalClientGetter not available for mention detection")
		return false
	}
	
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		log.Debug().Str("userID", userID).Msg("WhatsApp client not found for mention detection")
		return false
	}
	
	// Obter o JID da própria instância conectada
	if client.Store == nil || client.Store.ID == nil {
		log.Debug().
			Str("userID", userID).
			Msg("Client store or ID is nil - cannot get bot JID")
		return false
	}
	
	botJID := client.Store.ID.User + "@s.whatsapp.net"
	
	log.Info().
		Str("botJID", botJID).
		Str("userID", userID).
		Str("clientStoreUser", client.Store.ID.User).
		Msg("🔍 Bot JID obtained for mention detection")

	// 1. Verificar menções diretas em mensagens estendidas
	if evt.Message.ExtendedTextMessage != nil {
		contextInfo := evt.Message.ExtendedTextMessage.ContextInfo
		if contextInfo != nil && len(contextInfo.MentionedJID) > 0 {
			log.Info().
				Strs("mentionedJIDs", contextInfo.MentionedJID).
				Str("botJID", botJID).
				Int("totalMentions", len(contextInfo.MentionedJID)).
				Msg("🔍 Found mentions in extended text message")
			
			// Verificar se o bot está nas menções
			for i, mentionedJID := range contextInfo.MentionedJID {
				log.Info().
					Int("mentionIndex", i).
					Str("mentionedJID", mentionedJID).
					Str("botJID", botJID).
					Bool("isMatch", mentionedJID == botJID).
					Msg("🔍 Comparing mentioned JID with bot JID")
					
				if mentionedJID == botJID {
					log.Info().
						Str("mentionedJID", mentionedJID).
						Str("botJID", botJID).
						Str("messageID", evt.Info.ID).
						Str("groupJID", evt.Info.Chat.String()).
						Msg("🎯 DIRECT MENTION DETECTED - Bot was mentioned!")
					return true
				}
			}
			
			log.Info().
				Str("botJID", botJID).
				Strs("mentionedJIDs", contextInfo.MentionedJID).
				Msg("❌ Bot JID not found in mentions")
		}
		
	}
	
	// 2. Verificar menções em mensagens simples (contextInfo pode estar presente)
	if evt.Message.Conversation != nil {
		// Mensagens simples podem ter contextInfo também
		// Não precisa verificar texto, só menções diretas
		log.Debug().
			Str("text", *evt.Message.Conversation).
			Msg("Simple message - no mention context available")
	}
	
	log.Debug().
		Int("mentionedCount", func() int {
			if evt.Message.ExtendedTextMessage != nil && evt.Message.ExtendedTextMessage.ContextInfo != nil {
				return len(evt.Message.ExtendedTextMessage.ContextInfo.MentionedJID)
			}
			return 0
		}()).
		Msg("No mention detected")
	
	return false
}

// getGroupName busca o nome do grupo via whatsmeow
func getGroupName(groupJID, userID string) (string, error) {
	// Verificar se o GlobalClientGetter está disponível
	if GlobalClientGetter == nil {
		return "", fmt.Errorf("GlobalClientGetter not initialized")
	}
	
	// Obter cliente WhatsApp do usuário
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		return "", fmt.Errorf("WhatsApp client not found for user %s", userID)
	}
	
	// Parse do JID do grupo
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return "", fmt.Errorf("invalid group JID %s: %w", groupJID, err)
	}
	
	// Verificar se é realmente um grupo
	if jid.Server != "g.us" {
		return "", fmt.Errorf("JID %s is not a group", groupJID)
	}
	
	// Buscar metadados do grupo
	
	info, err := client.GetGroupInfo(jid)
	if err != nil {
		log.Debug().
			Str("groupJID", groupJID).
			Str("userID", userID).
			Err(err).
			Msg("Failed to get group info from WhatsApp")
		return "", fmt.Errorf("failed to get group info: %w", err)
	}
	
	if info != nil && info.Name != "" {
		log.Debug().
			Str("groupJID", groupJID).
			Str("groupName", info.Name).
			Msg("Successfully retrieved group name")
		return info.Name, nil
	}
	
	return "", fmt.Errorf("group name not available")
}

// getGroupParticipants busca participantes do grupo
func getGroupParticipants(groupJID, userID string) ([]types.JID, error) {
	if GlobalClientGetter == nil {
		return nil, fmt.Errorf("GlobalClientGetter not initialized")
	}
	
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		return nil, fmt.Errorf("WhatsApp client not found for user %s", userID)
	}
	
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return nil, fmt.Errorf("invalid group JID %s: %w", groupJID, err)
	}
	
	if jid.Server != "g.us" {
		return nil, fmt.Errorf("JID %s is not a group", groupJID)
	}
	
	
	info, err := client.GetGroupInfo(jid)
	if err != nil {
		return nil, fmt.Errorf("failed to get group info: %w", err)
	}
	
	if info == nil {
		return nil, fmt.Errorf("group info not available")
	}
	
	participants := make([]types.JID, 0, len(info.Participants))
	for _, participant := range info.Participants {
		participants = append(participants, participant.JID)
	}
	
	log.Debug().
		Str("groupJID", groupJID).
		Int("participantCount", len(participants)).
		Msg("Successfully retrieved group participants")
	
	return participants, nil
}

// formatPhoneNumber formata número para exibição amigável
func formatPhoneNumber(phone string) string {
	if phone == "" {
		return ""
	}
	
	// Remover caracteres não numéricos
	numericOnly := ""
	for _, char := range phone {
		if char >= '0' && char <= '9' {
			numericOnly += string(char)
		}
	}
	
	// Se não tem números, retornar original
	if numericOnly == "" {
		return phone
	}
	
	// Adicionar + no início se não tem
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + numericOnly
	}
	
	// Formatação específica para números brasileiros (+55)
	if strings.HasPrefix(numericOnly, "55") && len(numericOnly) >= 11 {
		// +55 (11) 99999-9999 ou +55 (11) 9999-9999
		ddd := numericOnly[2:4]
		numero := numericOnly[4:]
		
		if len(numero) == 9 { // Celular com 9 dígitos
			formatted := fmt.Sprintf("+55 (%s) %s-%s", ddd, numero[:5], numero[5:])
			return formatted
		} else if len(numero) == 8 { // Fixo com 8 dígitos
			formatted := fmt.Sprintf("+55 (%s) %s-%s", ddd, numero[:4], numero[4:])
			return formatted
		}
	}
	
	// Para outros países, formato simples
	return "+" + numericOnly
}

// parseIgnoreJids parseia lista de JIDs a ignorar
func parseIgnoreJids(ignoreJidsJSON string) []string {
	// Usar a função já implementada no models.go
	return ParseIgnoreJids(ignoreJidsJSON)
}

// getWhatsAppProfilePicture obtém a URL da imagem de perfil do WhatsApp
func getWhatsAppProfilePicture(phone string, userID string) string {
	if GlobalClientGetter == nil {
		log.Debug().Msg("GlobalClientGetter not initialized")
		return ""
	}
	
	// 1. Obter cliente WhatsApp do usuário
	whatsAppClient := GlobalClientGetter.GetWhatsmeowClient(userID)
	if whatsAppClient == nil {
		log.Debug().
			Str("phone", phone).
			Str("userID", userID).
			Msg("WhatsApp client not found for user")
		return ""
	}
	
	// 2. Construir JID do contato/grupo
	phoneWithoutPlus := strings.TrimPrefix(phone, "+")
	var jidString string
	
	// Detectar se é grupo ou individual
	if isGroupID(phoneWithoutPlus) {
		// Para grupos usar @g.us
		jidString = phoneWithoutPlus + "@g.us"
	} else {
		// Para individuais usar @s.whatsapp.net
		jidString = phoneWithoutPlus + "@s.whatsapp.net"
	}
	
	jid, err := types.ParseJID(jidString)
	if err != nil {
		log.Error().Err(err).
			Str("phone", phone).
			Str("jid_string", jidString).
			Str("userID", userID).
			Msg("Failed to parse JID for profile picture")
		return ""
	}
	
	// 3. Chamar GetProfilePictureInfo
	pic, err := whatsAppClient.GetProfilePictureInfo(jid, &whatsmeow.GetProfilePictureParams{
		Preview: false, // Obter imagem em alta resolução
	})
	if err != nil {
		log.Debug().Err(err).
			Str("phone", phone).
			Str("userID", userID).
			Str("jid", jidString).
			Bool("is_group", isGroupID(phoneWithoutPlus)).
			Msg("Failed to get profile picture (may not have one)")
		return ""
	}
	
	// 4. Retornar URL se disponível
	if pic != nil && pic.URL != "" {
		log.Info().
			Str("phone", phone).
			Str("userID", userID).
			Str("pic_id", pic.ID).
			Str("pic_url", pic.URL).
			Bool("is_group", isGroupID(phoneWithoutPlus)).
			Msg("Successfully retrieved WhatsApp profile picture")
		return pic.URL
	}
	
	log.Debug().
		Str("phone", phone).
		Str("userID", userID).
		Bool("is_group", isGroupID(phoneWithoutPlus)).
		Msg("Contact has no profile picture")
	
	return ""
}

// checkAndUpdateContactAvatar verifica se o avatar do contato mudou e atualiza se necessário
func checkAndUpdateContactAvatar(client *Client, contact *Contact, phone string, userID string) error {
	// 1. Obter avatar atual do WhatsApp
	currentAvatarURL := getWhatsAppProfilePicture(phone, userID)
	
	// 2. Comparar com avatar atual do contato no Chatwoot
	if currentAvatarURL == contact.AvatarURL {
		// Avatar não mudou
		log.Debug().
			Str("phone", phone).
			Int("contactID", contact.ID).
			Str("avatar_url", currentAvatarURL).
			Msg("Contact avatar unchanged")
		return nil
	}
	
	// 3. Avatar mudou, atualizar no Chatwoot
	log.Info().
		Str("phone", phone).
		Int("contactID", contact.ID).
		Str("old_avatar", contact.AvatarURL).
		Str("new_avatar", currentAvatarURL).
		Msg("Updating contact avatar in Chatwoot")
	
	updatedContact, err := client.UpdateContact(contact.ID, "", currentAvatarURL)
	if err != nil {
		return fmt.Errorf("failed to update contact avatar: %w", err)
	}
	
	// 4. Atualizar cache com dados atualizados
	GlobalCache.SetContact(phone, updatedContact)
	
	log.Info().
		Str("phone", phone).
		Int("contactID", contact.ID).
		Str("new_avatar", currentAvatarURL).
		Msg("Successfully updated contact avatar in Chatwoot")
	
	return nil
}

// processMediaMessage processa mensagem com mídia (conversas individuais)
func processMediaMessage(client *Client, config *Config, evt *events.Message, userID string, conversationID int, messageType string, db *sqlx.DB) error {
	// Obter cliente WhatsApp
	whatsAppClient := GlobalClientGetter.GetWhatsmeowClient(userID)
	if whatsAppClient == nil {
		log.Error().
			Str("userID", userID).
			Str("messageID", evt.Info.ID).
			Msg("WhatsApp client not found for media download")
		return fmt.Errorf("WhatsApp client not found")
	}

	// Download da mídia
	mediaData, err := downloadWhatsAppMedia(whatsAppClient, evt)
	if err != nil {
		log.Error().
			Err(err).
			Str("messageID", evt.Info.ID).
			Msg("Failed to download WhatsApp media")
		// Fallback: enviar mensagem de texto indicativa
		content := fmt.Sprintf("[%s não pôde ser baixado]", string(GetMediaType("application/octet-stream")))
		return sendTextMessageWithRetry(client, config, evt.Info.Chat.User, conversationID, content, evt.Info.ID, messageType)
	}

	// Enviar para Chatwoot
	chatwootMsg, err := client.SendMediaMessage(conversationID, mediaData, messageType, evt.Info.ID)
	if err != nil {
		log.Error().
			Err(err).
			Str("messageID", evt.Info.ID).
			Int("conversationID", conversationID).
			Str("fileName", mediaData.FileName).
			Msg("Failed to send media to Chatwoot")
		
		// Fallback: enviar caption como mensagem de texto se houver
		if mediaData.Caption != "" {
			return sendTextMessageWithRetry(client, config, evt.Info.Chat.User, conversationID, 
				fmt.Sprintf("[Mídia] %s", mediaData.Caption), evt.Info.ID, messageType)
		}
		
		return err
	}

	// Salvar mensagem de mídia no banco com vínculo do Chatwoot
	if chatwootMsg != nil {
		log.Debug().
			Str("messageID", evt.Info.ID).
			Int("chatwootID", chatwootMsg.ID).
			Str("fileName", mediaData.FileName).
			Str("mediaType", string(mediaData.MessageType)).
			Msg("🔄 About to save media message to database with Chatwoot ID")
		
		err = saveMediaMessageToDB(db, evt, userID, mediaData, chatwootMsg.ID, conversationID)
		if err != nil {
			log.Warn().Err(err).
				Str("messageID", evt.Info.ID).
				Int("chatwootID", chatwootMsg.ID).
				Str("mediaType", string(mediaData.MessageType)).
				Msg("Failed to save media message to database - continuing anyway")
		} else {
			log.Info().
				Str("messageID", evt.Info.ID).
				Int("chatwootID", chatwootMsg.ID).
				Str("fileName", mediaData.FileName).
				Str("mediaType", string(mediaData.MessageType)).
				Msg("✅ Successfully saved media message to database with Chatwoot ID")
		}
	} else {
		log.Warn().
			Str("messageID", evt.Info.ID).
			Str("mediaType", string(mediaData.MessageType)).
			Msg("⚠️ chatwootMsg is nil - cannot save Chatwoot ID to database")
	}

	log.Info().
		Str("messageID", evt.Info.ID).
		Int("conversationID", conversationID).
		Str("fileName", mediaData.FileName).
		Str("mediaType", string(mediaData.MessageType)).
		Int64("fileSize", mediaData.FileSize).
		Msg("Successfully processed media message")

	return nil
}

// processGroupMediaMessage processa mensagem com mídia em grupos
func processGroupMediaMessage(client *Client, config *Config, evt *events.Message, userID string, conversationID int, messageType string, textContent string, db *sqlx.DB) error {
	// Obter cliente WhatsApp
	whatsAppClient := GlobalClientGetter.GetWhatsmeowClient(userID)
	if whatsAppClient == nil {
		log.Error().
			Str("userID", userID).
			Str("messageID", evt.Info.ID).
			Msg("WhatsApp client not found for group media download")
		return fmt.Errorf("WhatsApp client not found")
	}

	// Download da mídia
	mediaData, err := downloadWhatsAppMedia(whatsAppClient, evt)
	if err != nil {
		log.Error().
			Err(err).
			Str("messageID", evt.Info.ID).
			Str("groupJID", evt.Info.Chat.String()).
			Msg("Failed to download WhatsApp group media")
		
		// Fallback: enviar mensagem de texto indicativa com info do remetente
		var fallbackContent string
		if !evt.Info.IsFromMe {
			senderPhone := evt.Info.Sender.User
			senderName := evt.Info.PushName
			
			if senderName == "" {
				senderName = "Desconhecido"
			}
			
			formattedPhone := formatPhoneNumber(senderPhone)
			senderInfo := fmt.Sprintf("%s - %s", formattedPhone, senderName)
			fallbackContent = fmt.Sprintf("**%s:**\n\n[Arquivo não pôde ser baixado]", senderInfo)
		} else {
			fallbackContent = "[Arquivo não pôde ser baixado]"
		}
		
		return sendTextMessageWithRetry(client, config, evt.Info.Chat.User, conversationID, fallbackContent, evt.Info.ID, messageType)
	}

	// Adicionar informações do remetente para mensagens de grupos
	if !evt.Info.IsFromMe {
		// Para mensagens de outros participantes, adicionar prefixo com info do remetente
		senderPhone := evt.Info.Sender.User
		senderName := evt.Info.PushName
		
		if senderName == "" {
			senderName = "Desconhecido"
		}
		
		formattedPhone := formatPhoneNumber(senderPhone)
		senderInfo := fmt.Sprintf("%s - %s", formattedPhone, senderName)
		
		// Se há caption original, combinar com info do remetente
		if mediaData.Caption != "" {
			mediaData.Caption = fmt.Sprintf("**%s:**\n\n%s", senderInfo, mediaData.Caption)
		} else if textContent != "" {
			// Se textContent já tem prefixo (vem do processamento de grupos)
			// Para stickers, substituir o conteúdo vazio por texto descritivo
			if mediaData.MessageType == MediaTypeSticker && strings.HasSuffix(textContent, ":\n\n") {
				mediaData.Caption = fmt.Sprintf("**%s:** Enviou um sticker", senderInfo)
			} else {
				mediaData.Caption = textContent
			}
		} else {
			// Para stickers sem prefixo (mensagens próprias), usar formato mais limpo
			if mediaData.MessageType == MediaTypeSticker {
				mediaData.Caption = fmt.Sprintf("**%s:** Enviou um sticker", senderInfo)
			} else {
				// Para outras mídias, formato genérico
				mediaData.Caption = fmt.Sprintf("**%s** enviou uma mídia", senderInfo)
			}
		}
	}
	// Para mensagens do próprio bot (IsFromMe=true), usar caption original ou conteúdo sem prefixo

	// Enviar para Chatwoot
	chatwootMsg, err := client.SendMediaMessage(conversationID, mediaData, messageType, evt.Info.ID)
	if err != nil {
		log.Error().
			Err(err).
			Str("messageID", evt.Info.ID).
			Str("groupJID", evt.Info.Chat.String()).
			Int("conversationID", conversationID).
			Str("fileName", mediaData.FileName).
			Msg("Failed to send group media to Chatwoot")
		
		// Fallback: enviar caption como mensagem de texto
		if mediaData.Caption != "" {
			return sendTextMessageWithRetry(client, config, evt.Info.Chat.User, conversationID, mediaData.Caption, evt.Info.ID, messageType)
		}
		
		return err
	}

	// Salvar mensagem de mídia de grupo no banco com vínculo do Chatwoot
	if chatwootMsg != nil {
		log.Debug().
			Str("messageID", evt.Info.ID).
			Int("chatwootID", chatwootMsg.ID).
			Str("groupJID", evt.Info.Chat.String()).
			Str("fileName", mediaData.FileName).
			Str("mediaType", string(mediaData.MessageType)).
			Msg("🔄 About to save group media message to database with Chatwoot ID")
		
		err = saveMediaMessageToDB(db, evt, userID, mediaData, chatwootMsg.ID, conversationID)
		if err != nil {
			log.Warn().Err(err).
				Str("messageID", evt.Info.ID).
				Int("chatwootID", chatwootMsg.ID).
				Str("groupJID", evt.Info.Chat.String()).
				Str("mediaType", string(mediaData.MessageType)).
				Msg("Failed to save group media message to database - continuing anyway")
		} else {
			log.Info().
				Str("messageID", evt.Info.ID).
				Int("chatwootID", chatwootMsg.ID).
				Str("groupJID", evt.Info.Chat.String()).
				Str("fileName", mediaData.FileName).
				Str("mediaType", string(mediaData.MessageType)).
				Msg("✅ Successfully saved group media message to database with Chatwoot ID")
		}
	} else {
		log.Warn().
			Str("messageID", evt.Info.ID).
			Str("groupJID", evt.Info.Chat.String()).
			Str("mediaType", string(mediaData.MessageType)).
			Msg("⚠️ chatwootMsg is nil - cannot save Chatwoot ID to database")
	}

	log.Info().
		Str("messageID", evt.Info.ID).
		Str("groupJID", evt.Info.Chat.String()).
		Int("conversationID", conversationID).
		Str("fileName", mediaData.FileName).
		Str("mediaType", string(mediaData.MessageType)).
		Int64("fileSize", mediaData.FileSize).
		Msg("Successfully processed group media message")

	return nil
}

// processReactionMessage processa reações para conversas individuais
func processReactionMessage(client *Client, config *Config, evt *events.Message, conversationID int, db *sqlx.DB, userID string) error {
	// Extrair informações da reação
	reaction := evt.Message.ReactionMessage
	if reaction == nil {
		return fmt.Errorf("reaction message is nil")
	}

	// Extrair emoji da reação (ou texto vazio para remoção)
	reactionEmoji := ""
	if reaction.Text != nil {
		reactionEmoji = *reaction.Text
	}
	
	// Construir mensagem de reação
	var content string
	if reactionEmoji == "" {
		content = "❌ _Reação removida_" // Remoção de reação
	} else {
		content = fmt.Sprintf("%s _reagiu à mensagem_", reactionEmoji)
	}

	// Determinar tipo da mensagem (incoming/outgoing)
	messageType := "incoming"
	if evt.Info.IsFromMe {
		messageType = "outgoing"
	}

	// Construir informações de reply para a mensagem reagida
	var replyInfo *ReplyInfo
	if reaction.Key != nil && reaction.Key.ID != nil && *reaction.Key.ID != "" {
		// Buscar mensagem original no banco para obter ChatwootMessageID
		originalMsg, err := FindMessageByStanzaID(db, *reaction.Key.ID, userID)
		if err != nil {
			log.Warn().Err(err).
				Str("reactedMessageID", *reaction.Key.ID).
				Msg("Failed to find reacted message - continuing without reply info")
		}
		
		replyInfo = &ReplyInfo{
			InReplyToExternalID: *reaction.Key.ID,
		}
		
		// Se encontrou a mensagem original E ela tem ChatwootMessageID, usar
		if originalMsg != nil && originalMsg.ChatwootMessageID != nil {
			replyInfo.InReplyToChatwootID = *originalMsg.ChatwootMessageID
		}
		
		log.Info().
			Str("reactedMessageID", *reaction.Key.ID).
			Str("currentMessageID", evt.Info.ID).
			Bool("found_original", originalMsg != nil).
			Bool("has_chatwoot_id", originalMsg != nil && originalMsg.ChatwootMessageID != nil).
			Msg("📩 Individual reaction with quote detected")
	}

	// Enviar para Chatwoot com reply info
	chatwootMsg, err := client.SendMessageWithReply(conversationID, content, messageType, evt.Info.ID, replyInfo)
	if err != nil {
		return fmt.Errorf("failed to send reaction message: %w", err)
	}
	
	// Atualizar mensagem no banco com chatwoot_message_id
	if chatwootMsg != nil && chatwootMsg.ID != 0 {
		err = UpdateMessageChatwootID(db, evt.Info.ID, chatwootMsg.ID, userID)
		if err != nil {
			log.Warn().Err(err).
				Str("whatsapp_id", evt.Info.ID).
				Int("chatwoot_id", chatwootMsg.ID).
				Msg("Failed to update message with Chatwoot ID")
		}
	}

	log.Info().
		Int("conversationID", conversationID).
		Str("reaction", reactionEmoji).
		Str("messageID", evt.Info.ID).
		Str("messageType", messageType).
		Bool("has_reply_info", replyInfo != nil).
		Msg("Processed reaction message")

	return nil
}

// processGroupReactionMessage processa reações em grupos com informações do remetente
func processGroupReactionMessage(client *Client, config *Config, evt *events.Message, conversationID int, db *sqlx.DB, userID string) error {
	// Extrair informações da reação
	reaction := evt.Message.ReactionMessage
	if reaction == nil {
		return fmt.Errorf("reaction message is nil")
	}

	// Extrair emoji da reação (ou texto vazio para remoção)
	reactionEmoji := ""
	if reaction.Text != nil {
		reactionEmoji = *reaction.Text
	}
	
	// Para grupos, adicionar informações do remetente
	var content string
	if !evt.Info.IsFromMe {
		// Para reações de outros participantes, adicionar info do remetente
		senderPhone := evt.Info.Sender.User
		senderName := evt.Info.PushName
		
		if senderName == "" {
			senderName = "Desconhecido"
		}
		
		// Formatar número de telefone
		formattedPhone := formatPhoneNumber(senderPhone)
		senderInfo := fmt.Sprintf("%s - %s", formattedPhone, senderName)
		
		if reactionEmoji == "" {
			content = fmt.Sprintf("**%s:** ❌ _removeu reação_", senderInfo)
		} else {
			content = fmt.Sprintf("**%s:** %s _reagiu à mensagem_", senderInfo, reactionEmoji)
		}
	} else {
		// Para reações próprias (IsFromMe=true)
		if reactionEmoji == "" {
			content = "❌ _Reação removida_"
		} else {
			content = fmt.Sprintf("%s _reagiu à mensagem_", reactionEmoji)
		}
	}

	// Determinar tipo da mensagem (incoming/outgoing)
	messageType := "incoming"
	if evt.Info.IsFromMe {
		messageType = "outgoing"
	}

	// Construir informações de reply para a mensagem reagida
	var replyInfo *ReplyInfo
	if reaction.Key != nil && reaction.Key.ID != nil && *reaction.Key.ID != "" {
		// Buscar mensagem original no banco para obter ChatwootMessageID
		originalMsg, err := FindMessageByStanzaID(db, *reaction.Key.ID, userID)
		if err != nil {
			log.Warn().Err(err).
				Str("reactedMessageID", *reaction.Key.ID).
				Msg("Failed to find reacted message - continuing without reply info")
		}
		
		replyInfo = &ReplyInfo{
			InReplyToExternalID: *reaction.Key.ID,
		}
		
		// Se encontrou a mensagem original E ela tem ChatwootMessageID, usar
		if originalMsg != nil && originalMsg.ChatwootMessageID != nil {
			replyInfo.InReplyToChatwootID = *originalMsg.ChatwootMessageID
		}
		
		log.Info().
			Str("reactedMessageID", *reaction.Key.ID).
			Str("currentMessageID", evt.Info.ID).
			Bool("found_original", originalMsg != nil).
			Bool("has_chatwoot_id", originalMsg != nil && originalMsg.ChatwootMessageID != nil).
			Msg("🎯 Group reaction with quote detected")
	}

	// Enviar para Chatwoot com reply info
	chatwootMsg, err := client.SendMessageWithReply(conversationID, content, messageType, evt.Info.ID, replyInfo)
	if err != nil {
		return fmt.Errorf("failed to send group reaction message: %w", err)
	}
	
	// Atualizar mensagem no banco com chatwoot_message_id
	if chatwootMsg != nil && chatwootMsg.ID != 0 {
		err = UpdateMessageChatwootID(db, evt.Info.ID, chatwootMsg.ID, userID)
		if err != nil {
			log.Warn().Err(err).
				Str("whatsapp_id", evt.Info.ID).
				Int("chatwoot_id", chatwootMsg.ID).
				Msg("Failed to update group reaction message with Chatwoot ID")
		}
	}

	log.Info().
		Int("conversationID", conversationID).
		Str("reaction", reactionEmoji).
		Str("groupJID", evt.Info.Chat.String()).
		Str("senderJID", evt.Info.Sender.String()).
		Str("messageID", evt.Info.ID).
		Str("messageType", messageType).
		Bool("has_reply_info", replyInfo != nil).
		Msg("Processed group reaction message")

	return nil
}

// saveMediaMessageToDB salva mensagem de mídia no banco com vínculo Chatwoot
func saveMediaMessageToDB(db *sqlx.DB, evt *events.Message, userID string, mediaData *MediaData, chatwootMessageID int, chatwootConversationID int) error {
	log.Debug().
		Str("whatsapp_id", evt.Info.ID).
		Str("user_id", userID).
		Int("chatwoot_id", chatwootMessageID).
		Int("chatwoot_conversation_id", chatwootConversationID).
		Str("media_type", string(mediaData.MessageType)).
		Str("file_name", mediaData.FileName).
		Msg("📥 saveMediaMessageToDB called for media")
	
	// Determinar nome do remetente
	senderName := evt.Info.PushName
	if senderName == "" {
		if evt.Info.IsFromMe {
			senderName = "Eu"
		} else {
			senderName = evt.Info.Chat.User // Fallback para número
		}
	}
	
	// Usar caption como conteúdo (se disponível)
	content := mediaData.Caption
	if content == "" {
		content = fmt.Sprintf("[%s: %s]", string(mediaData.MessageType), mediaData.FileName)
	}
	
	msg := MessageRecord{
		ID:                     evt.Info.ID,
		UserID:                 userID,
		Content:                content,
		SenderName:             senderName,
		MessageType:            string(mediaData.MessageType), // "image", "video", "audio", "document"
		ChatwootMessageID:      &chatwootMessageID,
		ChatwootConversationID: &chatwootConversationID,
		FromMe:                 evt.Info.IsFromMe,
		ChatJID:                evt.Info.Chat.String(),
	}
	
	return SaveMessage(db, msg)
}

// processWhatsAppMessageDeletion processa deleção de mensagem WhatsApp → Chatwoot
func processWhatsAppMessageDeletion(client *Client, config *Config, evt *events.Message, userID string, db *sqlx.DB) error {
	// Extrair ID da mensagem deletada do protocolMessage
	protocolMsg := evt.Message.GetProtocolMessage()
	if protocolMsg == nil || protocolMsg.Key == nil || protocolMsg.Key.ID == nil {
		return fmt.Errorf("invalid protocol message for deletion")
	}

	deletedMessageID := *protocolMsg.Key.ID
	log.Info().
		Str("deleted_message_id", deletedMessageID).
		Str("delete_event_id", evt.Info.ID).
		Str("chat", evt.Info.Chat.String()).
		Bool("from_me", evt.Info.IsFromMe).
		Msg("🗑️ Processing WhatsApp message deletion → Chatwoot")

	// 1. Buscar mensagem deletada no banco local
	originalMsg, err := FindMessageByStanzaID(db, deletedMessageID, userID)
	if err != nil {
		log.Error().Err(err).
			Str("deleted_message_id", deletedMessageID).
			Str("user_id", userID).
			Msg("Failed to find deleted message in database")
		return fmt.Errorf("failed to find deleted message: %w", err)
	}

	if originalMsg == nil {
		log.Warn().
			Str("deleted_message_id", deletedMessageID).
			Str("user_id", userID).
			Msg("⚠️ Deleted message not found in database - may not have been processed by Chatwoot")
		return nil // Não é erro crítico
	}

	if originalMsg.ChatwootMessageID == nil || originalMsg.ChatwootConversationID == nil {
		log.Warn().
			Str("deleted_message_id", deletedMessageID).
			Str("user_id", userID).
			Bool("has_message_id", originalMsg.ChatwootMessageID != nil).
			Bool("has_conversation_id", originalMsg.ChatwootConversationID != nil).
			Msg("⚠️ Deleted message missing Chatwoot IDs - was not sent to Chatwoot")
		return nil // Não é erro crítico
	}

	chatwootMsgID := *originalMsg.ChatwootMessageID
	chatwootConvID := *originalMsg.ChatwootConversationID
	log.Info().
		Str("deleted_message_id", deletedMessageID).
		Int("chatwoot_message_id", chatwootMsgID).
		Int("chatwoot_conversation_id", chatwootConvID).
		Str("original_content", originalMsg.Content).
		Str("sender", originalMsg.SenderName).
		Bool("was_from_me", originalMsg.FromMe).
		Msg("✅ Found original message with Chatwoot IDs")

	// 2. Deletar mensagem no Chatwoot
	err = client.DeleteMessage(chatwootConvID, chatwootMsgID)
	if err != nil {
		log.Error().Err(err).
			Int("chatwoot_message_id", chatwootMsgID).
			Str("deleted_message_id", deletedMessageID).
			Msg("Failed to delete message in Chatwoot")
		return fmt.Errorf("failed to delete message in Chatwoot: %w", err)
	}

	log.Info().
		Int("chatwoot_conversation_id", chatwootConvID).
		Int("chatwoot_message_id", chatwootMsgID).
		Str("deleted_message_id", deletedMessageID).
		Msg("✅ Successfully deleted message in Chatwoot")

	// 3. Remover mensagem do banco local
	err = DeleteMessage(db, deletedMessageID, userID)
	if err != nil {
		log.Error().Err(err).
			Str("deleted_message_id", deletedMessageID).
			Str("user_id", userID).
			Msg("Failed to delete message from local database")
		// Não retornar erro aqui pois a mensagem já foi deletada no Chatwoot
	} else {
		log.Info().
			Str("deleted_message_id", deletedMessageID).
			Int("chatwoot_conversation_id", chatwootConvID).
			Int("chatwoot_message_id", chatwootMsgID).
			Msg("✅ Successfully deleted message from local database")
	}

	// 4. Cache de mensagens processadas tem TTL automático, não precisa limpeza manual

	log.Info().
		Str("deleted_message_id", deletedMessageID).
		Int("chatwoot_conversation_id", chatwootConvID).
		Int("chatwoot_message_id", chatwootMsgID).
		Str("chat", evt.Info.Chat.String()).
		Msg("🎉 Successfully processed WhatsApp message deletion → Chatwoot")

	return nil
}