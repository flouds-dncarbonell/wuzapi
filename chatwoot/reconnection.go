package chatwoot

import (
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

// ReconnectionManager gerencia monitors de reconexão por usuário
type ReconnectionManager struct {
	activeMonitors map[string]chan bool // userID -> stop channel
	mutex          sync.RWMutex
	db             *sqlx.DB
}

// GlobalReconnectionManager instância global
var GlobalReconnectionManager *ReconnectionManager

// InitReconnectionManager inicializa o manager global
func InitReconnectionManager(db *sqlx.DB) {
	GlobalReconnectionManager = &ReconnectionManager{
		activeMonitors: make(map[string]chan bool),
		db:             db,
	}
	log.Info().Msg("🔄 ReconnectionManager initialized")
}

// StartMonitoring inicia monitor específico para um usuário desconectado
func (rm *ReconnectionManager) StartMonitoring(userID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	// Se já tem monitor ativo, não criar outro
	if _, exists := rm.activeMonitors[userID]; exists {
		log.Debug().Str("userID", userID).Msg("⚠️ Monitor already active for user")
		return
	}

	log.Info().Str("userID", userID).Msg("🔴 Starting reconnection monitor for user")

	// Criar canal de parada
	stopChan := make(chan bool)
	rm.activeMonitors[userID] = stopChan

	// Iniciar monitor específico em goroutine
	go rm.monitorUser(userID, stopChan)
}

// StopMonitoring para monitor específico de um usuário
func (rm *ReconnectionManager) StopMonitoring(userID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if stopChan, exists := rm.activeMonitors[userID]; exists {
		log.Info().Str("userID", userID).Msg("✅ Stopping reconnection monitor for user")
		close(stopChan)
		delete(rm.activeMonitors, userID)
	}
}

// monitorUser monitora reconexão de um usuário específico
func (rm *ReconnectionManager) monitorUser(userID string, stopChan chan bool) {
	log.Info().Str("userID", userID).Msg("🔍 Starting user-specific reconnection monitoring")

	// Monitor mais frequente quando desconectado (2 minutos)
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Usar clientManager global para verificar status
			client := GlobalClientGetter.GetWhatsmeowClient(userID)
			if client != nil && client.IsConnected() {
				// ✅ RECONECTOU! Para o monitor e notifica
				log.Info().Str("userID", userID).Msg("✅ User reconnected - stopping monitor")
				
				// Notificar reconexão no Chatwoot
				go rm.notifyReconnection(userID)
				
				// Remover monitor da lista
				rm.mutex.Lock()
				delete(rm.activeMonitors, userID)
				rm.mutex.Unlock()
				
				return // Para o monitor
			}

			// Ainda desconectado - continuar monitoramento
			log.Debug().Str("userID", userID).Msg("⚠️ User still disconnected - continuing monitor")

		case <-stopChan:
			// Canal para parar monitor manualmente
			log.Info().Str("userID", userID).Msg("🛑 Reconnection monitor stopped manually")
			return
		}
	}
}

// notifyDisconnection envia notificação de desconexão para Chatwoot
func (rm *ReconnectionManager) NotifyDisconnection(userID string) {
	log.Info().Str("userID", userID).Msg("📤 Sending disconnection notification to Chatwoot")

	// Buscar config do Chatwoot
	config, err := GetConfigByUserID(rm.db, userID)
	if err != nil {
		log.Debug().Err(err).Str("userID", userID).Msg("No Chatwoot config found - skipping notification")
		return
	}

	if !config.Enabled {
		log.Debug().Str("userID", userID).Msg("Chatwoot disabled - skipping notification")
		return
	}

	// Criar/encontrar self-conversation
	conversation, err := rm.createOrFindSelfConversation(config)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to create self-conversation")
		return
	}

	// Definir prioridade urgente
	client := NewClient(*config)
	err = client.SetConversationPriority(conversation.ID, "urgent")
	if err != nil {
		log.Warn().Err(err).Int("conversationID", conversation.ID).Msg("Failed to set urgent priority")
	}

	// Enviar mensagem de desconexão
	message := rm.buildDisconnectionMessage()
	err = rm.sendPrivateMessage(client, conversation.ID, message)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to send disconnection message")
		return
	}

	log.Info().Str("userID", userID).Int("conversationID", conversation.ID).Msg("📤 Disconnection notification sent successfully")
}

// notifyReconnection envia notificação de reconexão para Chatwoot
func (rm *ReconnectionManager) notifyReconnection(userID string) {
	log.Info().Str("userID", userID).Msg("📤 Sending reconnection notification to Chatwoot")

	// Buscar config do Chatwoot
	config, err := GetConfigByUserID(rm.db, userID)
	if err != nil {
		log.Debug().Err(err).Str("userID", userID).Msg("No Chatwoot config found - skipping notification")
		return
	}

	if !config.Enabled {
		log.Debug().Str("userID", userID).Msg("Chatwoot disabled - skipping notification")
		return
	}

	// Encontrar self-conversation existente
	conversation, err := rm.findSelfConversation(config)
	if err != nil || conversation == nil {
		log.Warn().Err(err).Str("userID", userID).Msg("Self-conversation not found - skipping reconnection notification")
		return
	}

	// Remover prioridade urgente
	client := NewClient(*config)
	err = client.SetConversationPriority(conversation.ID, "low")
	if err != nil {
		log.Warn().Err(err).Int("conversationID", conversation.ID).Msg("Failed to set low priority")
	}

	// Enviar mensagem de reconexão
	message := rm.buildReconnectionMessage()
	err = rm.sendPrivateMessage(client, conversation.ID, message)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to send reconnection message")
		return
	}

	log.Info().Str("userID", userID).Int("conversationID", conversation.ID).Msg("📤 Reconnection notification sent successfully")
}

// createOrFindSelfConversation cria ou encontra conversa com próprio número
func (rm *ReconnectionManager) createOrFindSelfConversation(config *Config) (*Conversation, error) {
	// Obter número do próprio bot
	whatsappClient := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if whatsappClient == nil || whatsappClient.Store == nil || whatsappClient.Store.ID == nil {
		return nil, fmt.Errorf("cannot get bot phone number")
	}

	botPhone := whatsappClient.Store.ID.User
	log.Debug().Str("botPhone", botPhone).Str("userID", config.UserID).Msg("Using bot phone for self-conversation")

	client := NewClient(*config)

	// Buscar inbox ID
	inbox, err := client.GetInboxByName(config.NameInbox)
	if err != nil || inbox == nil {
		return nil, fmt.Errorf("failed to get inbox: %w", err)
	}

	// Encontrar ou criar contato do próprio bot
	contact, err := client.FindContact(botPhone)
	if err != nil || contact == nil {
		log.Info().Str("botPhone", botPhone).Msg("Creating self-contact in Chatwoot")
		contact, err = client.CreateContact(botPhone, "🤖 Sistema WhatsApp", "", inbox.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to create self-contact: %w", err)
		}
	}

	// Buscar conversas existentes do contato
	conversations, err := client.ListContactConversations(contact.ID)
	if err == nil {
		for _, conv := range conversations {
			if conv.InboxID == inbox.ID {
				log.Debug().Int("conversationID", conv.ID).Msg("Found existing self-conversation")
				return &conv, nil
			}
		}
	}

	// Criar nova conversa
	log.Info().Int("contactID", contact.ID).Int("inboxID", inbox.ID).Msg("Creating new self-conversation")
	conversation, err := client.CreateConversation(contact.ID, inbox.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create self-conversation: %w", err)
	}

	return conversation, nil
}

// findSelfConversation encontra conversa existente com próprio número
func (rm *ReconnectionManager) findSelfConversation(config *Config) (*Conversation, error) {
	// Obter número do próprio bot
	whatsappClient := GlobalClientGetter.GetWhatsmeowClient(config.UserID)
	if whatsappClient == nil || whatsappClient.Store == nil || whatsappClient.Store.ID == nil {
		return nil, fmt.Errorf("cannot get bot phone number")
	}

	botPhone := whatsappClient.Store.ID.User
	client := NewClient(*config)

	// Buscar inbox ID
	inbox, err := client.GetInboxByName(config.NameInbox)
	if err != nil || inbox == nil {
		return nil, fmt.Errorf("failed to get inbox: %w", err)
	}

	// Encontrar contato
	contact, err := client.FindContact(botPhone)
	if err != nil || contact == nil {
		return nil, fmt.Errorf("self-contact not found")
	}

	// Buscar conversas existentes
	conversations, err := client.ListContactConversations(contact.ID)
	if err != nil {
		return nil, err
	}

	for _, conv := range conversations {
		if conv.InboxID == inbox.ID {
			return &conv, nil
		}
	}

	return nil, fmt.Errorf("self-conversation not found")
}

// sendPrivateMessage envia mensagem privada no Chatwoot
func (rm *ReconnectionManager) sendPrivateMessage(client *Client, conversationID int, message string) error {
	messageData := map[string]interface{}{
		"content":     message,
		"message_type": "incoming",
		"private":     true,
	}

	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", client.AccountID, conversationID)
	_, err := client.makeRequest("POST", endpoint, messageData)
	
	return err
}

// buildDisconnectionMessage constrói mensagem de desconexão
func (rm *ReconnectionManager) buildDisconnectionMessage() string {
	return fmt.Sprintf(
		"🔴 **WhatsApp Desconectado** 🔴\n\n"+
			"⏰ **Detectado em:** %s\n"+
			"🔄 **Status:** Aguardando reconexão\n\n"+
			"**Para reconectar:**\n"+
			"1️⃣ Digite `/qrcode` nesta conversa\n"+
			"2️⃣ Escaneie o QR code com seu celular\n"+
			"3️⃣ Aguarde a confirmação de conexão\n\n"+
			"⚠️ **IMPORTANTE:**\n"+
			"Mensagens enviadas/recebidas durante a desconexão podem ser perdidas.\n\n"+
			"_Monitor automático ativo - será notificado quando reconectar_",
		time.Now().Format("15:04:05"),
	)
}

// buildReconnectionMessage constrói mensagem de reconexão
func (rm *ReconnectionManager) buildReconnectionMessage() string {
	return fmt.Sprintf(
		"🎉 **WhatsApp Reconectado com Sucesso!** 🎉\n\n"+
			"✅ **Status:** Online e funcionando\n"+
			"⏰ **Reconectado em:** %s\n\n"+
			"⚠️ **IMPORTANTE:**\n"+
			"Mensagens enviadas/recebidas durante a desconexão podem ter sido perdidas.\n"+
			"Verifique com seus contatos se necessário.\n\n"+
			"🔄 Sincronização normal retomada.\n"+
			"_Monitor automático desativado_",
		time.Now().Format("15:04:05"),
	)
}

// GetActiveMonitors retorna lista de usuários com monitors ativos (para debug)
func (rm *ReconnectionManager) GetActiveMonitors() []string {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	var users []string
	for userID := range rm.activeMonitors {
		users = append(users, userID)
	}
	return users
}