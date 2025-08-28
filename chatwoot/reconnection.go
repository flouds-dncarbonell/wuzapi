package chatwoot

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
)

// ReconnectionManager gerencia monitors de reconexão por usuário
type ReconnectionManager struct {
	activeMonitors     map[string]chan bool      // userID -> stop channel
	notificationTimers map[string]*time.Ticker   // userID -> ticker 5min para notificações
	qrAttempts        map[string]int            // userID -> contador tentativas QR
	qrTimeout         map[string]*time.Timer    // userID -> timer 30s timeout QR
	lastQRTime        map[string]time.Time      // userID -> timestamp último QR enviado
	mutex             sync.RWMutex
	db                *sqlx.DB
}

// GlobalReconnectionManager instância global
var GlobalReconnectionManager *ReconnectionManager

// InitReconnectionManager inicializa o manager global
func InitReconnectionManager(db *sqlx.DB) {
	GlobalReconnectionManager = &ReconnectionManager{
		activeMonitors:     make(map[string]chan bool),
		notificationTimers: make(map[string]*time.Ticker),
		qrAttempts:        make(map[string]int),
		qrTimeout:         make(map[string]*time.Timer),
		lastQRTime:        make(map[string]time.Time),
		db:                db,
	}
	log.Info().Msg("🔄 ReconnectionManager initialized")
}

// EnsureMonitoring garante que monitor está ativo (idempotente)
func (rm *ReconnectionManager) EnsureMonitoring(userID string) {
	rm.mutex.RLock()
	_, exists := rm.activeMonitors[userID]
	rm.mutex.RUnlock()
	
	if exists {
		log.Debug().Str("userID", userID).Msg("🔄 Monitor already active - skipping")
		return
	}
	
	// Não tem monitor ativo - iniciar
	log.Info().Str("userID", userID).Msg("🎯 Disconnection detected via webhook error - starting monitor")
	rm.StartMonitoring(userID)
	
	// IMPORTANTE: Também notificar desconexão (mesmo fluxo do killchannel)
	go rm.NotifyDisconnection(userID)
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

	log.Info().Str("userID", userID).Msg("🔴 Starting disconnection monitoring for user")

	// Resetar contadores para nova desconexão
	rm.qrAttempts[userID] = 0
	delete(rm.lastQRTime, userID)
	
	// Parar qualquer timer QR anterior
	if timer, exists := rm.qrTimeout[userID]; exists {
		timer.Stop()
		delete(rm.qrTimeout, userID)
	}

	// Criar canal de parada
	stopChan := make(chan bool)
	rm.activeMonitors[userID] = stopChan

	// Criar ticker para notificações a cada 5 minutos
	notificationTicker := time.NewTicker(5 * time.Minute)
	rm.notificationTimers[userID] = notificationTicker

	// Iniciar monitor específico em goroutine
	go rm.monitorUserDisconnection(userID, stopChan, notificationTicker)
}

// StopMonitoring para monitor específico de um usuário
func (rm *ReconnectionManager) StopMonitoring(userID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if stopChan, exists := rm.activeMonitors[userID]; exists {
		log.Info().Str("userID", userID).Msg("✅ Stopping disconnection monitor for user")
		close(stopChan)
		delete(rm.activeMonitors, userID)
	}
	
	// Parar ticker de notificações
	if ticker, exists := rm.notificationTimers[userID]; exists {
		ticker.Stop()
		delete(rm.notificationTimers, userID)
	}
	
	// Parar timer QR se ativo
	if timer, exists := rm.qrTimeout[userID]; exists {
		timer.Stop()
		delete(rm.qrTimeout, userID)
	}
	
	// Limpar contadores
	delete(rm.qrAttempts, userID)
	delete(rm.lastQRTime, userID)
}

// monitorUserDisconnection monitora usuário desconectado com notificações periódicas
func (rm *ReconnectionManager) monitorUserDisconnection(userID string, stopChan chan bool, notificationTicker *time.Ticker) {
	log.Info().Str("userID", userID).Msg("🔍 Starting disconnection monitoring with 5min notifications")

	// Verificar status a cada 30 segundos (mais frequente para detectar reconexão)
	statusChecker := time.NewTicker(30 * time.Second)
	defer statusChecker.Stop()
	defer notificationTicker.Stop()

	for {
		select {
		case <-statusChecker.C:
			// Verificar se reconectou
			client := GlobalClientGetter.GetWhatsmeowClient(userID)
			if client != nil && client.IsConnected() && client.IsLoggedIn() {
				// ✅ RECONECTOU! Para o monitor e notifica
				log.Info().Str("userID", userID).Msg("✅ User reconnected - stopping monitor")
				
				// Notificar reconexão no Chatwoot
				go rm.notifyReconnection(userID)
				
				// Parar monitor (será removido da lista pelo StopMonitoring)
				go rm.StopMonitoring(userID)
				return
			}

		case <-notificationTicker.C:
			// Enviar notificação periódica a cada 5 minutos
			log.Info().Str("userID", userID).Msg("📴 Sending periodic disconnection notification")
			go rm.sendPeriodicDisconnectionNotification(userID)

		case <-stopChan:
			// Canal para parar monitor manualmente
			log.Info().Str("userID", userID).Msg("🛑 Disconnection monitor stopped manually")
			return
		}
	}
}

// NotifyDisconnection envia notificação inicial de desconexão para Chatwoot
func (rm *ReconnectionManager) NotifyDisconnection(userID string) {
	log.Info().Str("userID", userID).Msg("📤 Sending initial disconnection notification to Chatwoot")

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

	// Definir prioridade MÁXIMA 
	client := NewClient(*config)
	err = client.SetConversationPriority(conversation.ID, "urgent")
	if err != nil {
		log.Warn().Err(err).Int("conversationID", conversation.ID).Msg("Failed to set urgent priority")
	} else {
		log.Info().Int("conversationID", conversation.ID).Msg("❗ Conversation priority set to URGENT")
	}

	// Enviar mensagem de desconexão inicial
	message := rm.buildInitialDisconnectionMessage(config.UserID)
	err = rm.sendPrivateMessage(client, conversation.ID, message)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to send initial disconnection message")
		return
	}

	log.Info().Str("userID", userID).Int("conversationID", conversation.ID).Msg("📤 Initial disconnection notification sent successfully")
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

	// Remover prioridade urgente (volta ao normal)
	client := NewClient(*config)
	err = client.SetConversationPriority(conversation.ID, "none")
	if err != nil {
		log.Warn().Err(err).Int("conversationID", conversation.ID).Msg("Failed to set none priority")
	} else {
		log.Info().Int("conversationID", conversation.ID).Msg("✅ Conversation priority restored to NONE")
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
	// Obter número do próprio bot via banco (mais confiável quando desconectado)
	botPhone := rm.getBotPhoneFromDB(config.UserID)
	if botPhone == "" {
		return nil, fmt.Errorf("cannot get bot phone number from database")
	}
	
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
	// Obter número do próprio bot via banco
	botPhone := rm.getBotPhoneFromDB(config.UserID)
	if botPhone == "" {
		return nil, fmt.Errorf("cannot get bot phone number from database")
	}
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

// buildInitialDisconnectionMessage constrói mensagem inicial de desconexão
func (rm *ReconnectionManager) buildInitialDisconnectionMessage(userID string) string {
	// Tentar obter número do telefone
	phoneNumber := rm.getPhoneNumber(userID)
	
	return fmt.Sprintf(
		"🔴 **WhatsApp Desconectado** 🔴\n\n"+
			"📱 **Número:** %s\n"+
			"⏰ **Detectado em:** %s\n"+
			"🔄 **Status:** Aguardando reconexão\n\n"+
			"**Para reconectar:**\n"+
			"1️⃣ Digite `#qrcode` nesta conversa\n"+
			"2️⃣ Escaneie o QR code com seu celular **%s**\n"+
			"3️⃣ Aguarde a confirmação de conexão\n\n"+
			"⚠️ **IMPORTANTE:**\n"+
			"Mensagens enviadas/recebidas durante a desconexão podem ser perdidas.\n\n"+
			"🗺 **Notificações automáticas:** A cada 5 minutos\n"+
			"_Monitor ativo - será notificado quando reconectar_",
		phoneNumber,
		time.Now().Format("15:04:05"),
		phoneNumber,
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

// sendPeriodicDisconnectionNotification envia notificação periódica (a cada 5 min)
func (rm *ReconnectionManager) sendPeriodicDisconnectionNotification(userID string) {
	// Buscar config do Chatwoot
	config, err := GetConfigByUserID(rm.db, userID)
	if err != nil || !config.Enabled {
		log.Debug().Str("userID", userID).Msg("Skipping periodic notification - no config or disabled")
		return
	}

	// Encontrar self-conversation
	conversation, err := rm.findSelfConversation(config)
	if err != nil || conversation == nil {
		log.Warn().Err(err).Str("userID", userID).Msg("Cannot find self-conversation for periodic notification")
		return
	}

	// Construir mensagem periódica
	message := rm.buildPeriodicDisconnectionMessage(userID)
	
	// Enviar mensagem
	client := NewClient(*config)
	err = rm.sendPrivateMessage(client, conversation.ID, message)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to send periodic disconnection message")
		return
	}

	log.Info().Str("userID", userID).Int("conversationID", conversation.ID).Msg("🗺 Periodic disconnection notification sent")
}

// buildPeriodicDisconnectionMessage constrói mensagem periódica de desconexão
func (rm *ReconnectionManager) buildPeriodicDisconnectionMessage(userID string) string {
	// Verificar quantas tentativas QR já foram feitas
	rm.mutex.RLock()
	attempts := rm.qrAttempts[userID]
	rm.mutex.RUnlock()
	
	// Obter número do telefone
	phoneNumber := rm.getPhoneNumber(userID)

	if attempts >= 5 {
		// Limite excedido - mensagem diferente
		return fmt.Sprintf(
			"⚠️ **WhatsApp Ainda Desconectado** ⚠️\n\n"+
				"📱 **Número:** %s\n"+
				"⏰ **Status em:** %s\n"+
				"🔄 **Tentativas QR:** %d/5 (limite excedido)\n\n"+
				"🚫 **Limite de tentativas QR automáticas excedido**\n\n"+
				"**Para gerar novo QR code:**\n"+
				"Digite `#qrcode` nesta conversa\n\n"+
				"⚠️ **IMPORTANTE:**\n"+
				"- Verifique se o celular **%s** tem internet\n"+
				"- Certifique-se que o WhatsApp está atualizado\n"+
				"- Tente reiniciar o WhatsApp no celular\n\n"+
				"_Notificações continuam a cada 5 minutos_",
			phoneNumber,
			time.Now().Format("15:04:05"),
			attempts,
			phoneNumber,
		)
	}
	
	// Mensagem periódica normal
	return fmt.Sprintf(
		"⚠️ **WhatsApp Ainda Desconectado** ⚠️\n\n"+
			"📱 **Número:** %s\n"+
			"⏰ **Status em:** %s\n"+
			"🔄 **Tentativas QR:** %d/5\n\n"+
			"**Para reconectar:**\n"+
			"Digite `#qrcode` nesta conversa para gerar QR\n\n"+
			"⚠️ **IMPORTANTE:**\n"+
			"Mensagens enviadas/recebidas durante a desconexão podem ser perdidas.\n\n"+
			"_Próxima notificação em 5 minutos_",
		phoneNumber,
		time.Now().Format("15:04:05"),
		attempts,
	)
}

// HandleQRCodeRequest processa solicitação de QR code com sistema de tentativas
func (rm *ReconnectionManager) HandleQRCodeRequest(userID string, conversationID int, config *Config) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	// Verificar limite de tentativas
	if rm.qrAttempts[userID] >= 5 {
		log.Warn().Str("userID", userID).Int("attempts", rm.qrAttempts[userID]).Msg("QR generation limit exceeded")
		return rm.sendLimitExceededMessage(conversationID, config)
	}

	// Incrementar contador
	rm.qrAttempts[userID]++
	rm.lastQRTime[userID] = time.Now()

	// Parar timer anterior se existe
	if timer, exists := rm.qrTimeout[userID]; exists {
		timer.Stop()
	}

	log.Info().Str("userID", userID).Int("attempt", rm.qrAttempts[userID]).Msg("🔥 Generating QR code - attempt")

	// Gerar QR code ativo (forçar reconexão)
	err := rm.generateActiveQRCode(userID, conversationID, config)
	if err != nil {
		return fmt.Errorf("failed to generate QR code: %w", err)
	}

	// Criar timer de 30 segundos para verificar conexão
	qrTimer := time.AfterFunc(30*time.Second, func() {
		rm.handleQRTimeout(userID, conversationID, config)
	})
	rm.qrTimeout[userID] = qrTimer

	return nil
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

// getPhoneNumber obtém o número de telefone do usuário
func (rm *ReconnectionManager) getPhoneNumber(userID string) string {
	// Método 1: Tentar via WhatsApp Client (se ainda estiver disponível na memória)
	if GlobalClientGetter != nil {
		client := GlobalClientGetter.GetWhatsmeowClient(userID)
		if client != nil && client.Store != nil && client.Store.ID != nil {
			return "+" + client.Store.ID.User
		}
	}
	
	// Método 2: Buscar no banco de dados (campo jid)
	var jid string
	err := rm.db.Get(&jid, "SELECT jid FROM users WHERE id = $1", userID)
	if err == nil && jid != "" {
		// Extrair número do JID (formato: 555197173288:23@s.whatsapp.net)
		if strings.Contains(jid, "@") {
			phoneNumber := strings.Split(jid, "@")[0]
			// Remover sufixo :23 se existir
			if strings.Contains(phoneNumber, ":") {
				phoneNumber = strings.Split(phoneNumber, ":")[0]
			}
			if phoneNumber != "" {
				return "+" + phoneNumber
			}
		}
	}
	
	// Método 3: Fallback - buscar no cache userinfo
	// (implementação adicional se necessário)
	
	// Fallback final
	return "[Número não disponível]"
}

// getBotPhoneFromDB obtém número do bot via banco de dados (mais confiável)
func (rm *ReconnectionManager) getBotPhoneFromDB(userID string) string {
	var jid string
	err := rm.db.Get(&jid, "SELECT jid FROM users WHERE id = $1", userID)
	if err != nil {
		log.Warn().Err(err).Str("userID", userID).Msg("Failed to get JID from database")
		return ""
	}
	
	if jid == "" {
		log.Warn().Str("userID", userID).Msg("Empty JID in database")
		return ""
	}
	
	// Extrair número do JID (formato: 5511999999999@s.whatsapp.net)
	if strings.Contains(jid, "@") {
		phoneNumber := strings.Split(jid, "@")[0]
		// Remover sufixo :22 se existir (555197173288:22 -> 555197173288)
		if strings.Contains(phoneNumber, ":") {
			phoneNumber = strings.Split(phoneNumber, ":")[0]
		}
		return phoneNumber
	}
	
	return ""
}

// generateActiveQRCode gera QR code forçando nova conexão WhatsApp
func (rm *ReconnectionManager) generateActiveQRCode(userID string, conversationID int, config *Config) error {
	// NOVO: Garantir que cliente existe antes de gerar QR
	err := rm.ensureClientExists(userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to ensure client exists for QR generation")
		return fmt.Errorf("failed to ensure client exists: %w", err)
	}
	
	// Verificar se client existe (agora deveria existir)
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client == nil {
		return fmt.Errorf("client still not available after creation attempt for user %s", userID)
	}

	// Se já conectado e logado, não precisa gerar QR
	if client.IsConnected() && client.IsLoggedIn() {
		message := "✅ **WhatsApp já está conectado!**\n\nNão é necessário gerar QR code."
		chatwootClient := NewClient(*config)
		return rm.sendPrivateMessage(chatwootClient, conversationID, message)
	}

	// Forçar logout se necessário para gerar novo QR
	if client.IsLoggedIn() {
		log.Info().Str("userID", userID).Msg("Forcing logout to generate fresh QR")
		err := client.Logout(context.Background())
		if err != nil {
			log.Warn().Err(err).Str("userID", userID).Msg("Failed to logout - proceeding anyway")
		}
		time.Sleep(1 * time.Second) // Aguardar logout
	}

	// Conectar para gerar QR
	if !client.IsConnected() {
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect client: %w", err)
		}
	}

	// Aguardar um pouco para QR ser gerado e salvo no banco pelo sistema principal
	time.Sleep(2 * time.Second)
	
	// Buscar QR code do banco (já foi salvo pelo sistema principal)
	qrCode, err := rm.getQRCodeFromDB(userID)
	if err != nil {
		return fmt.Errorf("failed to get QR code from database: %w", err)
	}
	
	if qrCode == "" {
		return fmt.Errorf("QR code not found in database for user %s", userID)
	}
	
	// Enviar QR code diretamente para Chatwoot
	err = rm.sendQRCodeMessage(conversationID, qrCode, config, rm.qrAttempts[userID])
	if err != nil {
		return fmt.Errorf("failed to send QR code message: %w", err)
	}

	return nil
}

// processQRChannel processa eventos do canal QR
func (rm *ReconnectionManager) processQRChannel(userID string, conversationID int, config *Config, qrChan <-chan whatsmeow.QRChannelItem) {
	chatwootClient := NewClient(*config)
	
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			// QR code gerado - enviar para Chatwoot
			log.Info().Str("userID", userID).Msg("QR code generated - sending to Chatwoot")
			
			// Converter QR para base64 se necessário
			qrData := evt.Code
			if !strings.HasPrefix(qrData, "data:image/") {
				// Gerar imagem QR a partir do código
				qrImage, err := qrcode.Encode(qrData, qrcode.Medium, 256)
				if err != nil {
					log.Error().Err(err).Str("userID", userID).Msg("Failed to encode QR image")
					continue
				}
				qrData = "data:image/png;base64," + base64.StdEncoding.EncodeToString(qrImage)
			}
			
			// Enviar QR como mensagem
			err := rm.sendQRCodeMessage(conversationID, qrData, config, rm.qrAttempts[userID])
			if err != nil {
				log.Error().Err(err).Str("userID", userID).Msg("Failed to send QR code message")
			}

		case "success":
			// Conectado com sucesso
			log.Info().Str("userID", userID).Msg("QR pairing successful")
			
			// Parar timer de timeout
			rm.mutex.Lock()
			if timer, exists := rm.qrTimeout[userID]; exists {
				timer.Stop()
				delete(rm.qrTimeout, userID)
			}
			rm.mutex.Unlock()
			
			// Enviar mensagem de sucesso
			message := "🎉 **WhatsApp Conectado com Sucesso!**\n\n✅ QR code escaneado e autenticado\n🔄 Sincronização em andamento..."
			rm.sendPrivateMessage(chatwootClient, conversationID, message)
			
			return // Parar processamento

		case "timeout":
			// QR expirou
			log.Warn().Str("userID", userID).Msg("QR code timeout")
			return // Parar processamento (timeout será tratado pelo timer de 30s)

		default:
			log.Debug().Str("userID", userID).Str("event", evt.Event).Msg("QR channel event")
		}
	}
}

// handleQRTimeout trata timeout de 30s após geração do QR
func (rm *ReconnectionManager) handleQRTimeout(userID string, conversationID int, config *Config) {
	log.Info().Str("userID", userID).Msg("QR timeout reached - checking connection")
	
	// Verificar se conectou
	client := GlobalClientGetter.GetWhatsmeowClient(userID)
	if client != nil && client.IsConnected() && client.IsLoggedIn() {
		log.Info().Str("userID", userID).Msg("User connected during QR timeout - success!")
		return // Já conectou, não fazer nada
	}
	
	rm.mutex.Lock()
	attempts := rm.qrAttempts[userID]
	rm.mutex.Unlock()
	
	chatwootClient := NewClient(*config)
	
	if attempts >= 5 {
		// Limite atingido
		log.Warn().Str("userID", userID).Int("attempts", attempts).Msg("QR attempt limit reached")
		rm.sendLimitExceededMessage(conversationID, config)
		return
	}
	
	// Ainda tem tentativas - enviar mensagem de timeout
	message := fmt.Sprintf(
		"⏰ **QR Code Expirado** (%d/5)\n\n"+
			"O QR code expirou em 30 segundos sem ser escaneado.\n\n"+
			"🔄 **Novo QR será enviado automaticamente em breve...**\n\n"+
			"**Dicas:**\n"+
			"• Tenha o celular pronto antes do próximo QR\n"+
			"• Verifique se o celular tem internet estável\n"+
			"• Certifique-se que o WhatsApp está atualizado",
		attempts,
	)
	
	rm.sendPrivateMessage(chatwootClient, conversationID, message)
}

// sendLimitExceededMessage envia mensagem quando limite de tentativas é excedido
func (rm *ReconnectionManager) sendLimitExceededMessage(conversationID int, config *Config) error {
	message := "🚫 **Limite de Tentativas Excedido** 🚫\n\n" +
		"Foram realizadas 5 tentativas de QR code sem sucesso.\n\n" +
		"**Status da Conexão:** Desconectado\n\n" +
		"**Para gerar novo QR code:**\n" +
		"Digite `#qrcode` nesta conversa\n\n" +
		"**Se continuar com problemas:**\n" +
		"❌ **Erro ao conectar, por favor, contate o suporte para mais instruções**\n\n" +
		"_Notificações continuam a cada 5 minutos_"
	
	chatwootClient := NewClient(*config)
	return rm.sendPrivateMessage(chatwootClient, conversationID, message)
}

// sendQRCodeMessage envia QR code como mensagem no Chatwoot usando multipart/form-data
func (rm *ReconnectionManager) sendQRCodeMessage(conversationID int, qrCodeData string, config *Config, attempt int) error {
	// Remover prefixo base64 se existe
	if strings.HasPrefix(qrCodeData, "data:image/png;base64,") {
		qrCodeData = strings.TrimPrefix(qrCodeData, "data:image/png;base64,")
	}
	
	// Decodificar base64 para bytes
	qrImageData, err := base64.StdEncoding.DecodeString(qrCodeData)
	if err != nil {
		return fmt.Errorf("failed to decode base64 QR data: %w", err)
	}
	
	client := NewClient(*config)
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", client.AccountID, conversationID)
	
	// Criar multipart form (mesmo padrão do SendMediaMessage)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Adicionar campos obrigatórios
	if err := writer.WriteField("message_type", "incoming"); err != nil {
		return fmt.Errorf("failed to write message_type: %w", err)
	}
	
	if err := writer.WriteField("private", "true"); err != nil {
		return fmt.Errorf("failed to write private: %w", err)
	}

	// Adicionar conteúdo da mensagem
	content := fmt.Sprintf(
		"📱 **QR Code para Conexão** (Tentativa %d/5)\n\n"+
			"**Instruções:**\n"+
			"1️⃣ Abra o WhatsApp no seu celular\n"+
			"2️⃣ Toque em ⋮ (menu) > Aparelhos conectados\n"+
			"3️⃣ Toque em 'Conectar um aparelho'\n"+
			"4️⃣ Escaneie este código RAPIDAMENTE\n\n"+
			"⏰ **Expira em 30 segundos**\n"+
			"🔄 Se expirar, será enviado automaticamente outro QR",
		attempt,
	)
	
	if err := writer.WriteField("content", content); err != nil {
		return fmt.Errorf("failed to write content: %w", err)
	}

	// Adicionar arquivo QR com MIME type correto (mesmo padrão do sistema)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="attachments[]"; filename="qrcode.png"`)
	h.Set("Content-Type", "image/png")
	fileWriter, err := writer.CreatePart(h)
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := fileWriter.Write(qrImageData); err != nil {
		return fmt.Errorf("failed to write QR image data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Fazer requisição HTTP (mesmo padrão do sistema)
	req, err := client.createMultipartRequest("POST", endpoint, &body, writer.FormDataContentType())
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send QR message: %w", err)
	}
	defer resp.Body.Close()

	if err := client.handleError(resp); err != nil {
		return fmt.Errorf("failed to send QR code: %w", err)
	}
	
	log.Info().Int("conversationID", conversationID).Int("attempt", attempt).Msg("📱 QR code sent to Chatwoot via multipart")
	return nil
}

// ensureClientExists garante que um cliente WhatsApp existe para o usuário
// Implementação híbrida que reutiliza a lógica existente sem HTTP
func (rm *ReconnectionManager) ensureClientExists(userID string) error {
	// Verificar se já existe
	if GlobalClientGetter != nil {
		if client := GlobalClientGetter.GetWhatsmeowClient(userID); client != nil {
			log.Debug().Str("userID", userID).Msg("WhatsApp client already exists")
			return nil
		}
	}
	
	log.Info().Str("userID", userID).Msg("🔄 Creating WhatsApp client for QR generation")
	
	// Buscar dados necessários no banco (mesmo que handlers.go faz)
	jid, err := rm.getBotJIDFromDB(userID)
	if err != nil {
		return fmt.Errorf("failed to get JID from database: %w", err)
	}
	
	token, err := rm.getUserTokenFromDB(userID)
	if err != nil {
		return fmt.Errorf("failed to get token from database: %w", err)
	}
	
	subscribedEvents, err := rm.getUserSubscribedEvents(userID)
	if err != nil {
		log.Warn().Err(err).Msg("Could not get subscribed events, using defaults")
		subscribedEvents = []string{} // Default vazio
	}
	
	// Preparar killchannel (mesmo que handlers.go faz)
	rm.ensureKillChannel(userID)
	
	// CHAMAR DIRETAMENTE a função startClient via interface
	go rm.startClientDirect(userID, jid, token, subscribedEvents)
	
	// Aguardar criação com timeout inteligente
	return rm.waitForClientCreation(userID, 15*time.Second)
}


// getBotJIDFromDB obtém JID do bot via banco de dados
func (rm *ReconnectionManager) getBotJIDFromDB(userID string) (string, error) {
	var jid string
	err := rm.db.Get(&jid, "SELECT jid FROM users WHERE id = $1", userID)
	if err != nil {
		return "", err
	}
	if jid == "" {
		return "", fmt.Errorf("empty JID for user %s", userID)
	}
	return jid, nil
}

// getUserTokenFromDB obtém token do usuário via banco de dados
func (rm *ReconnectionManager) getUserTokenFromDB(userID string) (string, error) {
	var token string
	err := rm.db.Get(&token, "SELECT token FROM users WHERE id = $1", userID)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("empty token for user %s", userID)
	}
	return token, nil
}

// getUserSubscribedEvents busca eventos subscritos do usuário
func (rm *ReconnectionManager) getUserSubscribedEvents(userID string) ([]string, error) {
	// Implementação simplificada para reconexão - usar defaults
	// Em produção, poderia chamar a função do helpers.go se necessário
	return []string{}, nil
}

// ensureKillChannel garante que killchannel existe para o usuário
func (rm *ReconnectionManager) ensureKillChannel(userID string) {
	// Preparar para criação do cliente - killchannel será criado no startClient
	log.Debug().Str("userID", userID).Msg("Preparing for client creation")
}

// startClientDirect inicia cliente diretamente usando interfaces globais
func (rm *ReconnectionManager) startClientDirect(userID, jid, token string, subscribedEvents []string) {
	log.Info().Str("userID", userID).Str("jid", jid).Msg("🚀 Starting WhatsApp client directly for reconnection")
	
	// Usar as mesmas interfaces globais que o sistema principal usa
	err := rm.createClientDirectly(userID, jid, token, subscribedEvents)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to create client directly")
		return
	}
	
	log.Info().Str("userID", userID).Msg("✅ Client creation process initiated")
}

// createClientDirectly cria o cliente usando as interfaces globais existentes
func (rm *ReconnectionManager) createClientDirectly(userID, jid, token string, subscribedEvents []string) error {
	// Verificar se GlobalClientStarter está disponível
	if GlobalClientStarter == nil {
		return fmt.Errorf("GlobalClientStarter not initialized")
	}
	
	log.Info().Str("userID", userID).Str("jid", jid).Msg("Creating WhatsApp client through GlobalClientStarter")
	
	// USAR A INTERFACE DIRETA - mesmo processo que /session/connect!
	err := GlobalClientStarter.StartClient(userID, jid, token, subscribedEvents)
	if err != nil {
		return fmt.Errorf("failed to start client: %w", err)
	}
	
	log.Info().Str("userID", userID).Msg("✅ Client creation initiated successfully")
	return nil
}


// waitForClientCreation aguarda criação do cliente com timeout
func (rm *ReconnectionManager) waitForClientCreation(userID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	checkInterval := 500 * time.Millisecond
	
	log.Debug().Str("userID", userID).Dur("timeout", timeout).Msg("⏳ Waiting for WhatsApp client creation")
	
	for time.Now().Before(deadline) {
		if GlobalClientGetter != nil {
			if client := GlobalClientGetter.GetWhatsmeowClient(userID); client != nil {
				log.Info().Str("userID", userID).Msg("✅ WhatsApp client created successfully")
				return nil
			}
		}
		time.Sleep(checkInterval)
	}
	
	return fmt.Errorf("timeout: WhatsApp client not created after %v for user %s", timeout, userID)
}

// getQRCodeFromDB busca QR code salvo no banco pelo sistema principal
func (rm *ReconnectionManager) getQRCodeFromDB(userID string) (string, error) {
	var qrCode string
	err := rm.db.Get(&qrCode, "SELECT qrcode FROM users WHERE id = $1", userID)
	if err != nil {
		return "", fmt.Errorf("failed to get QR code from database: %w", err)
	}
	return qrCode, nil
}