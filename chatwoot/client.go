package chatwoot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Client representa um cliente para interagir com a API do Chatwoot
type Client struct {
	BaseURL    string
	AccountID  string
	Token      string
	UserAgent  string
	Timeout    time.Duration
	httpClient *http.Client
}

// NewClient cria uma nova instância do cliente Chatwoot
func NewClient(config Config) *Client {
	return &Client{
		BaseURL:   config.URL,
		AccountID: config.AccountID,
		Token:     config.Token,
		UserAgent: "wuzapi-chatwoot/1.0",
		Timeout:   30 * time.Second,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// makeRequest executa uma requisição HTTP para a API do Chatwoot
func (c *Client) makeRequest(method, endpoint string, body interface{}) (*http.Response, error) {
	// Construir URL completa
	baseURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	
	baseURL.Path = path.Join(baseURL.Path, endpoint)
	requestURL := baseURL.String()

	// Preparar body da requisição
	var requestBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("error marshaling request body: %w", err)
		}
		requestBody = bytes.NewBuffer(jsonData)
	}

	// Criar requisição
	req, err := http.NewRequest(method, requestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Adicionar headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("api_access_token", c.Token)

	log.Debug().
		Str("method", method).
		Str("url", requestURL).
		Str("content_type", req.Header.Get("Content-Type")).
		Str("user_agent", req.Header.Get("User-Agent")).
		Bool("has_auth", c.Token != "").
		Msg("Making Chatwoot API request")

	// Executar requisição
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error executing request: %w", err)
	}

	return resp, nil
}

// handleError processa erros da API Chatwoot
func (c *Client) handleError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("HTTP %d: failed to read error response", resp.StatusCode)
	}

	// Log da resposta completa para debug
	log.Error().
		Int("status_code", resp.StatusCode).
		Str("response_body", string(body)).
		Str("content_type", resp.Header.Get("Content-Type")).
		Msg("Chatwoot API returned error response")

	// Tentar parsear erro JSON
	var errorResponse map[string]interface{}
	if err := json.Unmarshal(body, &errorResponse); err == nil {
		if message, ok := errorResponse["message"]; ok {
			return fmt.Errorf("HTTP %d: %v", resp.StatusCode, message)
		}
		if errors, ok := errorResponse["errors"]; ok {
			return fmt.Errorf("HTTP %d: %v", resp.StatusCode, errors)
		}
	}

	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

// TestConnection testa a conectividade com o Chatwoot
func (c *Client) TestConnection() error {
	log.Info().
		Str("base_url", c.BaseURL).
		Str("account_id", c.AccountID).
		Bool("has_token", c.Token != "").
		Msg("=== STARTING CHATWOOT CLIENT TEST CONNECTION ===")

	endpoint := fmt.Sprintf("/api/v1/accounts/%s/inboxes", c.AccountID)
	log.Info().
		Str("endpoint", endpoint).
		Str("full_url_will_be", c.BaseURL+endpoint).
		Msg("About to make request to Chatwoot")

	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		log.Error().
			Err(err).
			Str("base_url", c.BaseURL).
			Str("endpoint", endpoint).
			Msg("Error in makeRequest")
		return fmt.Errorf("connection test failed: %w", err)
	}
	defer resp.Body.Close()

	log.Info().
		Int("status_code", resp.StatusCode).
		Str("status", resp.Status).
		Msg("Received response from Chatwoot")

	if err := c.handleError(resp); err != nil {
		log.Error().
			Err(err).
			Int("status_code", resp.StatusCode).
			Str("account_id", c.AccountID).
			Str("base_url", c.BaseURL).
			Msg("Chatwoot connection test failed")
		return fmt.Errorf("connection test failed: %w", err)
	}

	log.Info().Msg("Chatwoot connection test successful")
	return nil
}

// TestConnectionWithInbox testa conectividade e valida se inbox específico existe
func (c *Client) TestConnectionWithInbox(inboxName string) error {
	log.Info().
		Str("base_url", c.BaseURL).
		Str("account_id", c.AccountID).
		Str("inbox_name", inboxName).
		Bool("has_token", c.Token != "").
		Msg("=== STARTING CHATWOOT CONNECTION TEST WITH INBOX VALIDATION ===")

	// 1. Testar conectividade básica
	if err := c.TestConnection(); err != nil {
		return err
	}

	// 2. Se inbox name foi especificado, validar se existe
	if inboxName != "" {
		log.Info().
			Str("inbox_name", inboxName).
			Msg("Validating configured inbox name exists")

		inbox, err := c.GetInboxByName(inboxName)
		if err != nil {
			log.Error().
				Err(err).
				Str("inbox_name", inboxName).
				Msg("Error searching for configured inbox")
			return fmt.Errorf("failed to search for inbox '%s': %w", inboxName, err)
		}

		if inbox == nil {
			log.Warn().
				Str("inbox_name", inboxName).
				Msg("Configured inbox not found in Chatwoot")
			return fmt.Errorf("inbox '%s' not found in Chatwoot account", inboxName)
		}

		log.Info().
			Str("inbox_name", inboxName).
			Int("inbox_id", inbox.ID).
			Str("channel_type", inbox.Name).
			Msg("✅ Configured inbox found and validated")
	} else {
		log.Info().Msg("No specific inbox configured - using first available")
	}

	log.Info().
		Str("inbox_name", inboxName).
		Msg("Chatwoot connection and inbox validation successful")
	
	return nil
}


// FindContact busca um contato pelo número de telefone/ID usando query otimizada
func (c *Client) FindContact(phone string) (*Contact, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/filter", c.AccountID)
	
	// Normalizar número de telefone (remover + se tiver)
	phoneWithoutPlus := strings.TrimPrefix(phone, "+")
	
	// Detectar se é grupo ou conversa individual
	isGroup := isGroupID(phoneWithoutPlus)
	
	var identifier string
	var filterPayload []map[string]interface{}
	
	if isGroup {
		// Para grupos: buscar apenas por identifier
		identifier = phoneWithoutPlus + "@g.us"
		
		log.Debug().
			Str("group_id", phoneWithoutPlus).
			Str("identifier", identifier).
			Msg("Starting group contact search")
		
		filterPayload = []map[string]interface{}{
			{
				"attribute_key":   "identifier",
				"filter_operator": "equal_to",
				"values":          []string{identifier},
				"query_operator":  nil,
			},
		}
	} else {
		// Para conversas individuais: buscar por phone_number OR identifier
		identifier = phoneWithoutPlus + "@s.whatsapp.net"
		
		log.Debug().
			Str("original_phone", phone).
			Str("phone_clean", phoneWithoutPlus).
			Str("identifier", identifier).
			Msg("Starting individual contact search with OR query")
		
		filterPayload = []map[string]interface{}{
			{
				"attribute_key":   "phone_number",
				"filter_operator": "equal_to",
				"values":          []string{phoneWithoutPlus}, // SEM + como chatwoot-lib
				"query_operator":  "OR",
			},
			{
				"attribute_key":   "identifier",
				"filter_operator": "equal_to",
				"values":          []string{identifier}, // sem + para identifier
				"query_operator":  nil, // Último item sempre null
			},
		}
	}
	
	payload := map[string]interface{}{
		"payload": filterPayload,
	}
	
	log.Debug().
		Interface("filter_payload", filterPayload).
		Msg("Executing optimized contact filter with OR")

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("error executing optimized filter request: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var searchResult struct {
		Payload []Contact `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("error decoding optimized filter response: %w", err)
	}

	log.Debug().
		Int("results_count", len(searchResult.Payload)).
		Str("phone_clean", phoneWithoutPlus).
		Str("identifier", identifier).
		Bool("is_group", isGroup).
		Msg("Contact filter results")

	// Retornar primeiro contato encontrado ou nil
	if len(searchResult.Payload) > 0 {
		contact := &searchResult.Payload[0]
		log.Info().
			Int("contactID", contact.ID).
			Str("found_by", "phone_number OR identifier").
			Str("contact_phone", contact.PhoneNumber).
			Str("contact_identifier", contact.Identifier).
			Msg("Contact found with optimized OR query")
		return contact, nil
	}
	
	log.Debug().
		Str("phone_clean", phoneWithoutPlus).
		Str("identifier", identifier).
		Bool("is_group", isGroup).
		Msg("Contact not found")
	
	return nil, nil // Não encontrado
}


// CreateContact cria um novo contato no Chatwoot
func (c *Client) CreateContact(phone, name, avatarURL string, inboxID int) (*Contact, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts", c.AccountID)
	
	// Validar dados de entrada
	if phone == "" {
		return nil, fmt.Errorf("phone number/ID is required")
	}
	if name == "" {
		name = phone // Usar phone/ID como fallback
	}
	if inboxID <= 0 {
		return nil, fmt.Errorf("invalid inboxID: %d", inboxID)
	}
	
	// Normalizar número de telefone/ID (remover + se tiver)
	phoneWithoutPlus := strings.TrimPrefix(phone, "+")
	
	// Detectar se é grupo ou conversa individual
	isGroup := isGroupID(phoneWithoutPlus)
	
	var identifier string
	payload := map[string]interface{}{
		"inbox_id": inboxID,
		"name":     name,
	}
	
	if isGroup {
		// Para grupos: apenas identifier, SEM phone_number
		identifier = phoneWithoutPlus + "@g.us"
		payload["identifier"] = identifier
		
		log.Debug().
			Str("group_id", phoneWithoutPlus).
			Str("group_name", name).
			Str("identifier", identifier).
			Int("inbox_id", inboxID).
			Msg("Creating group contact (no phone_number field)")
	} else {
		// Para conversas individuais: incluir phone_number COM + e identifier SEM +
		normalizedPhone := phoneWithoutPlus
		if !strings.HasPrefix(normalizedPhone, "+") {
			normalizedPhone = "+" + normalizedPhone
		}
		identifier = phoneWithoutPlus + "@s.whatsapp.net"
		
		payload["phone_number"] = normalizedPhone
		payload["identifier"] = identifier
		
		log.Debug().
			Str("original_phone", phone).
			Str("normalized_phone", normalizedPhone).
			Str("identifier", identifier).
			Int("inbox_id", inboxID).
			Msg("Creating individual contact (with phone_number field)")
	}

	if avatarURL != "" {
		payload["avatar_url"] = avatarURL
	}

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("error creating contact: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result struct {
		Payload Contact `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding create contact response: %w", err)
	}

	return &result.Payload, nil
}

// UpdateContact atualiza um contato existente no Chatwoot
func (c *Client) UpdateContact(contactID int, name, avatarURL string) (*Contact, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d", c.AccountID, contactID)
	
	payload := make(map[string]interface{})
	
	if name != "" {
		payload["name"] = name
	}
	
	if avatarURL != "" {
		payload["avatar_url"] = avatarURL
	}
	
	// Se não há nada para atualizar, retornar erro
	if len(payload) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}
	
	log.Debug().
		Int("contactID", contactID).
		Interface("payload", payload).
		Str("endpoint", endpoint).
		Msg("Updating contact in Chatwoot")

	resp, err := c.makeRequest("PUT", endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("error updating contact: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result struct {
		Payload Contact `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding update contact response: %w", err)
	}

	log.Info().
		Int("contactID", contactID).
		Str("name", name).
		Str("avatar_url", avatarURL).
		Msg("Successfully updated contact in Chatwoot")

	return &result.Payload, nil
}

// FindConversation busca uma conversa ativa para o contato
func (c *Client) FindConversation(contactID, inboxID int) (*Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d/conversations", c.AccountID, contactID)
	
	log.Debug().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("endpoint", endpoint).
		Msg("Searching for existing conversation")
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("error finding conversation: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result struct {
		Payload []Conversation `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding conversations response: %w", err)
	}

	log.Debug().
		Int("conversations_found", len(result.Payload)).
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Msg("Found conversations for contact")

	// Buscar conversa ativa no inbox especificado (seguindo biblioteca oficial)
	for _, conversation := range result.Payload {
		if conversation.InboxID == inboxID && conversation.Status != "resolved" {
			log.Info().
				Int("conversationID", conversation.ID).
				Int("contactID", contactID).
				Int("inboxID", inboxID).
				Str("status", conversation.Status).
				Msg("Found existing conversation")
			return &conversation, nil
		}
	}

	log.Debug().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Msg("No active conversation found for contact in inbox")

	return nil, nil // Não encontrado
}

// CreateConversation cria uma nova conversa
func (c *Client) CreateConversation(contactID, inboxID int) (*Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations", c.AccountID)
	
	// Validar IDs antes de criar payload
	if contactID <= 0 {
		return nil, fmt.Errorf("invalid contactID: %d", contactID)
	}
	if inboxID <= 0 {
		return nil, fmt.Errorf("invalid inboxID: %d", inboxID)
	}
	
	// VALIDAÇÃO ADICIONAL: Verificar se o contato existe antes de criar conversa
	log.Debug().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Msg("Validating contact exists before creating conversation")
	
	// CORREÇÃO: Seguindo chatwoot-lib (IDs como strings, SEM source_id)
	payload := map[string]interface{}{
		"contact_id": strconv.Itoa(contactID), // String como na chatwoot-lib
		"inbox_id":   strconv.Itoa(inboxID),   // String como na chatwoot-lib
	}
	
	// Adicionar status apenas se conversation_pending (como chatwoot-lib)
	// Por padrão, Chatwoot cria como "open"

	log.Debug().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Interface("payload", payload).
		Str("endpoint", endpoint).
		Msg("Creating conversation in Chatwoot")

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		log.Error().
			Err(err).
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Str("endpoint", endpoint).
			Msg("Error making request to create conversation")
		return nil, fmt.Errorf("error creating conversation: %w", err)
	}
	defer resp.Body.Close()

	log.Debug().
		Int("status_code", resp.StatusCode).
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("status", resp.Status).
		Msg("Received response from Chatwoot create conversation")

	if err := c.handleError(resp); err != nil {
		log.Error().
			Int("status_code", resp.StatusCode).
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Interface("sent_payload", payload).
			Str("endpoint", endpoint).
			Msg("Chatwoot API error creating conversation")
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	var result Conversation
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Error().
			Err(err).
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Msg("Error decoding create conversation response")
		return nil, fmt.Errorf("error decoding create conversation response: %w", err)
	}

	log.Info().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Int("conversationID", result.ID).
		Str("status", result.Status).
		Msg("Successfully created conversation in Chatwoot")

	return &result, nil
}

// ListContactConversations lista todas as conversas de um contato (como chatwoot-lib)
func (c *Client) ListContactConversations(contactID int) ([]Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d/conversations", c.AccountID, contactID)
	
	log.Debug().
		Int("contactID", contactID).
		Str("endpoint", endpoint).
		Msg("Listing contact conversations")
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("error listing contact conversations: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result struct {
		Payload []Conversation `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding contact conversations response: %w", err)
	}

	log.Debug().
		Int("conversations_count", len(result.Payload)).
		Int("contactID", contactID).
		Msg("Listed contact conversations")

	return result.Payload, nil
}

// ToggleConversationStatus muda status da conversa (como chatwoot-lib)
func (c *Client) ToggleConversationStatus(conversationID int, status string) error {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/toggle_status", c.AccountID, conversationID)
	
	payload := map[string]interface{}{
		"status": status,
	}
	
	log.Debug().
		Int("conversationID", conversationID).
		Str("status", status).
		Msg("Toggling conversation status")

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return fmt.Errorf("error toggling conversation status: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return err
	}

	log.Info().
		Int("conversationID", conversationID).
		Str("status", status).
		Msg("Successfully toggled conversation status")

	return nil
}

// CreateConversationWithSource cria uma nova conversa com source_id
func (c *Client) CreateConversationWithSource(contactID, inboxID int, sourceID string) (*Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations", c.AccountID)
	
	// Validar IDs antes de criar payload
	if contactID <= 0 {
		return nil, fmt.Errorf("invalid contactID: %d", contactID)
	}
	if inboxID <= 0 {
		return nil, fmt.Errorf("invalid inboxID: %d", inboxID)
	}
	if sourceID == "" {
		return nil, fmt.Errorf("sourceID is required")
	}
	
	// Payload baseado na documentação oficial da API Chatwoot
	payload := map[string]interface{}{
		"contact_id": contactID,
		"inbox_id":   inboxID,
		"source_id":  sourceID, // Obrigatório para WhatsApp
		"status":     "open",
	}

	log.Debug().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("sourceID", sourceID).
		Interface("payload", payload).
		Str("endpoint", endpoint).
		Msg("Creating conversation with source_id in Chatwoot")

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		log.Error().
			Err(err).
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Str("sourceID", sourceID).
			Str("endpoint", endpoint).
			Msg("Error making request to create conversation with source")
		return nil, fmt.Errorf("error creating conversation: %w", err)
	}
	defer resp.Body.Close()

	log.Debug().
		Int("status_code", resp.StatusCode).
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("sourceID", sourceID).
		Str("status", resp.Status).
		Msg("Received response from Chatwoot create conversation with source")

	if err := c.handleError(resp); err != nil {
		log.Error().
			Int("status_code", resp.StatusCode).
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Str("sourceID", sourceID).
			Interface("sent_payload", payload).
			Str("endpoint", endpoint).
			Msg("Chatwoot API error creating conversation with source")
		return nil, fmt.Errorf("failed to create conversation: %w", err)
	}

	var result Conversation
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Error().
			Err(err).
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Str("sourceID", sourceID).
			Msg("Error decoding create conversation response")
		return nil, fmt.Errorf("error decoding create conversation response: %w", err)
	}

	log.Info().
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("sourceID", sourceID).
		Int("conversationID", result.ID).
		Str("status", result.Status).
		Msg("Successfully created conversation with source_id in Chatwoot")

	return &result, nil
}

// SendMessage envia uma mensagem para uma conversa
func (c *Client) SendMessage(conversationID int, content string, messageType string, sourceID string) (*Message, error) {
	return c.SendMessageWithReply(conversationID, content, messageType, sourceID, nil)
}

// SendMessageWithReply envia uma mensagem para uma conversa com suporte a replies
func (c *Client) SendMessageWithReply(conversationID int, content string, messageType string, sourceID string, replyInfo *ReplyInfo) (*Message, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", c.AccountID, conversationID)
	
	payload := map[string]interface{}{
		"content":      content,
		"message_type": messageType, // "incoming" ou "outgoing"
	}

	if sourceID != "" {
		payload["source_id"] = sourceID
	}

	// Adicionar content_attributes se for uma reply
	if replyInfo != nil {
		contentAttrs := map[string]interface{}{}
		if replyInfo.InReplyToExternalID != "" {
			contentAttrs["in_reply_to_external_id"] = replyInfo.InReplyToExternalID
		}
		if replyInfo.InReplyToChatwootID != 0 {
			contentAttrs["in_reply_to"] = replyInfo.InReplyToChatwootID
		}
		if len(contentAttrs) > 0 {
			payload["content_attributes"] = contentAttrs
		}
	}

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("error sending message: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result MessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding send message response: %w", err)
	}

	// Converter MessageResponse para Message (retorno da função)
	message := &Message{
		ID:             result.ID,
		Content:        result.Content,
		MessageType:    convertIntToMessageType(result.MessageType),
		ConversationID: result.ConversationID,
		SenderID:       result.SenderID,
		SourceID:       result.SourceID,
	}

	return message, nil
}

// convertIntToMessageType converte int para string no message_type
func convertIntToMessageType(messageType int) string {
	switch messageType {
	case 0:
		return "incoming"
	case 1:
		return "outgoing"
	default:
		return "incoming" // fallback
	}
}

// SendMediaMessage envia uma mensagem com mídia para uma conversa
func (c *Client) SendMediaMessage(conversationID int, mediaData *MediaData, messageType string, sourceID string) (*Message, error) {
	if mediaData == nil {
		return nil, fmt.Errorf("media data is required")
	}

	// Validar dados de mídia
	if err := ValidateMediaData(mediaData); err != nil {
		return nil, fmt.Errorf("invalid media data: %w", err)
	}

	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", c.AccountID, conversationID)
	
	// Criar multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Adicionar campos obrigatórios
	if err := writer.WriteField("message_type", messageType); err != nil {
		return nil, fmt.Errorf("failed to write message_type: %w", err)
	}

	// Adicionar caption se presente
	if mediaData.Caption != "" {
		if err := writer.WriteField("content", mediaData.Caption); err != nil {
			return nil, fmt.Errorf("failed to write content: %w", err)
		}
	}

	// Adicionar source_id se presente
	if sourceID != "" {
		if err := writer.WriteField("source_id", sourceID); err != nil {
			return nil, fmt.Errorf("failed to write source_id: %w", err)
		}
	}

	// Adicionar arquivo com MIME type correto
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachments[]"; filename="%s"`, mediaData.FileName))
	h.Set("Content-Type", mediaData.MimeType)
	fileWriter, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(fileWriter, CreateMediaReader(mediaData)); err != nil {
		return nil, fmt.Errorf("failed to write media data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Fazer requisição HTTP
	req, err := c.createMultipartRequest("POST", endpoint, &body, writer.FormDataContentType())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send media message: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result MessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding send media response: %w", err)
	}

	log.Info().
		Int("conversationID", conversationID).
		Str("fileName", mediaData.FileName).
		Str("mediaType", string(mediaData.MessageType)).
		Str("mimeType", mediaData.MimeType).
		Int64("fileSize", mediaData.FileSize).
		Int("messageID", result.ID).
		Msg("Successfully sent media message to Chatwoot")

	// Converter para Message
	message := &Message{
		ID:             result.ID,
		Content:        result.Content,
		MessageType:    convertIntToMessageType(result.MessageType),
		ConversationID: result.ConversationID,
		SenderID:       result.SenderID,
		SourceID:       result.SourceID,
	}

	return message, nil
}

// createMultipartRequest cria requisição HTTP com multipart/form-data
func (c *Client) createMultipartRequest(method, endpoint string, body io.Reader, contentType string) (*http.Request, error) {
	// Construir URL completa
	baseURL, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	// Limpar endpoint se começar com /
	if strings.HasPrefix(endpoint, "/") {
		endpoint = strings.TrimPrefix(endpoint, "/")
	}

	fullURL := baseURL.ResolveReference(&url.URL{Path: endpoint})

	// Criar requisição
	req, err := http.NewRequest(method, fullURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Headers obrigatórios
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Api-Access-Token", c.Token)
	req.Header.Set("User-Agent", c.UserAgent)

	return req, nil
}

// ListInboxes lista todos os inboxes da conta
func (c *Client) ListInboxes() ([]Inbox, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/inboxes", c.AccountID)
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("error listing inboxes: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result struct {
		Payload []Inbox `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding inboxes response: %w", err)
	}

	return result.Payload, nil
}

// GetInboxByName busca um inbox pelo nome
func (c *Client) GetInboxByName(name string) (*Inbox, error) {
	inboxes, err := c.ListInboxes()
	if err != nil {
		return nil, err
	}

	for _, inbox := range inboxes {
		if inbox.Name == name {
			return &inbox, nil
		}
	}

	return nil, nil // Não encontrado
}

// GetInboxID retorna o ID do primeiro inbox disponível
func (c *Client) GetInboxID() (int, error) {
	inboxes, err := c.ListInboxes()
	if err != nil {
		return 0, err
	}

	if len(inboxes) == 0 {
		return 0, fmt.Errorf("no inboxes found")
	}

	return inboxes[0].ID, nil
}

// SendAttachment envia um anexo para uma conversa
func (c *Client) SendAttachment(conversationID int, content, filename string, fileData []byte, messageType string) (*Message, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", c.AccountID, conversationID)
	
	// Criar multipart form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	
	// Adicionar content se fornecido
	if content != "" {
		if err := writer.WriteField("content", content); err != nil {
			return nil, fmt.Errorf("error writing content field: %w", err)
		}
	}
	
	// Adicionar message_type
	if err := writer.WriteField("message_type", messageType); err != nil {
		return nil, fmt.Errorf("error writing message_type field: %w", err)
	}
	
	// Detectar MIME type baseado na extensão do arquivo
	contentType := "application/octet-stream" // default
	if ext := strings.ToLower(path.Ext(filename)); ext != "" {
		switch ext {
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".png":
			contentType = "image/png"
		case ".gif":
			contentType = "image/gif"
		case ".webp":
			contentType = "image/webp"
		case ".mp4":
			contentType = "video/mp4"
		case ".webm":
			contentType = "video/webm"
		case ".mp3":
			contentType = "audio/mpeg"
		case ".ogg":
			contentType = "audio/ogg"
		case ".wav":
			contentType = "audio/wav"
		case ".pdf":
			contentType = "application/pdf"
		case ".doc":
			contentType = "application/msword"
		case ".docx":
			contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case ".txt":
			contentType = "text/plain"
		}
	}

	// Adicionar arquivo com MIME type correto
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachments[]"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("error creating form file: %w", err)
	}
	
	if _, err := part.Write(fileData); err != nil {
		return nil, fmt.Errorf("error writing file data: %w", err)
	}
	
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("error closing multipart writer: %w", err)
	}
	
	// Fazer requisição HTTP manual para multipart
	req, err := http.NewRequest("POST", c.BaseURL+endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	
	req.Header.Set("api_access_token", c.Token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", c.UserAgent)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending attachment: %w", err)
	}
	defer resp.Body.Close()
	
	if err := c.handleError(resp); err != nil {
		return nil, err
	}
	
	var result Message
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding send attachment response: %w", err)
	}
	
	return &result, nil
}

// SetConversationPriority define a prioridade de uma conversa
func (c *Client) SetConversationPriority(conversationID int, priority string) error {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/toggle_priority", c.AccountID, conversationID)
	
	// Validar prioridade
	validPriorities := map[string]bool{
		"urgent": true,
		"high":   true,
		"medium": true,
		"low":    true,
		"none":   true,
	}
	
	if !validPriorities[priority] {
		return fmt.Errorf("invalid priority '%s'. Valid options: urgent, high, medium, low, none", priority)
	}
	
	payload := map[string]interface{}{
		"priority": priority,
	}

	log.Debug().
		Int("conversationID", conversationID).
		Str("priority", priority).
		Msg("Setting conversation priority in Chatwoot")

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return fmt.Errorf("error setting conversation priority: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return fmt.Errorf("Chatwoot API error setting priority: %w", err)
	}
	
	log.Debug().
		Int("conversationID", conversationID).
		Str("priority", priority).
		Msg("Successfully set conversation priority in Chatwoot")

	return nil
}

// UpdateLastSeen atualiza último acesso na conversa (confirmação de leitura)
// Usa endpoint oficial seguindo padrão da chatwoot-lib
func (c *Client) UpdateLastSeen(conversationID int, contactID int, inboxID int) error {
	// 1. Buscar inbox_identifier
	inboxIdentifier, err := c.getInboxIdentifier(inboxID)
	if err != nil {
		return fmt.Errorf("error getting inbox identifier: %w", err)
	}
	
	// 2. Buscar sourceId do contato
	sourceID, err := c.getContactSourceID(contactID, inboxID)
	if err != nil {
		return fmt.Errorf("error getting contact source ID: %w", err)
	}
	
	// 3. Usar endpoint oficial da chatwoot-lib
	endpoint := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/update_last_seen", 
		inboxIdentifier, sourceID, conversationID)

	log.Debug().
		Int("conversationID", conversationID).
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("inboxIdentifier", inboxIdentifier).
		Str("sourceID", sourceID).
		Msg("Updating last seen in Chatwoot conversation with official endpoint")

	resp, err := c.makeRequest("POST", endpoint, nil)
	if err != nil {
		return fmt.Errorf("error updating last seen: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return fmt.Errorf("Chatwoot API error updating last seen: %w", err)
	}
	
	log.Info().
		Int("conversationID", conversationID).
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("inboxIdentifier", inboxIdentifier).
		Str("sourceID", sourceID).
		Msg("Successfully updated last seen in Chatwoot with official endpoint")

	return nil
}

// getInboxIdentifier busca o inbox_identifier de um inbox (com cache)
// Baseado na documentação: inbox_identifier vem do payload de inboxes
func (c *Client) getInboxIdentifier(inboxID int) (string, error) {
	// Verificar cache primeiro
	cacheKey := fmt.Sprintf("inbox_identifier_%d", inboxID)
	if identifier, found := GlobalCache.GetCachedData(cacheKey); found {
		log.Debug().
			Int("inboxID", inboxID).
			Str("identifier", identifier.(string)).
			Msg("Found inbox_identifier in cache")
		return identifier.(string), nil
	}
	// Buscar todos os inboxes para encontrar o inbox_identifier
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/inboxes", c.AccountID)
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("error getting inboxes: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return "", fmt.Errorf("Chatwoot API error getting inboxes: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading inboxes response: %w", err)
	}

	var response struct {
		Payload []struct {
			ID              int    `json:"id"`
			InboxIdentifier string `json:"inbox_identifier"`
		} `json:"payload"`
	}
	
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("error unmarshaling inboxes response: %w", err)
	}
	
	// Buscar o inbox com ID correspondente
	for _, inbox := range response.Payload {
		if inbox.ID == inboxID {
			if inbox.InboxIdentifier == "" {
				return "", fmt.Errorf("inbox_identifier not found for inbox %d", inboxID)
			}
			
			// Armazenar no cache (TTL de 1 hora)
			GlobalCache.SetCachedData(cacheKey, inbox.InboxIdentifier, 3600)
			
			log.Debug().
				Int("inboxID", inboxID).
				Str("identifier", inbox.InboxIdentifier).
				Msg("Cached inbox_identifier")
				
			return inbox.InboxIdentifier, nil
		}
	}
	
	return "", fmt.Errorf("inbox %d not found in account", inboxID)
}

// getSourceIDFromWebhookCache busca source_id do cache do webhook primeiro
func (c *Client) getSourceIDFromWebhookCache(conversationID int) (string, bool) {
	cacheKey := fmt.Sprintf("source_id_conv_%d", conversationID)
	if sourceID, found := GlobalCache.GetCachedData(cacheKey); found {
		log.Debug().
			Int("conversationID", conversationID).
			Str("sourceID", sourceID.(string)).
			Msg("Found source_id from webhook cache")
		return sourceID.(string), true
	}
	return "", false
}

// getContactSourceID busca o source_id de um contato em um inbox específico (com cache)
// Baseado na documentação: source_id vem do contact_inbox das conversas
func (c *Client) getContactSourceID(contactID int, inboxID int) (string, error) {
	// Verificar cache primeiro
	cacheKey := fmt.Sprintf("source_id_%d_%d", contactID, inboxID)
	if sourceID, found := GlobalCache.GetCachedData(cacheKey); found {
		log.Debug().
			Int("contactID", contactID).
			Int("inboxID", inboxID).
			Str("sourceID", sourceID.(string)).
			Msg("Found source_id in cache")
		return sourceID.(string), nil
	}
	// Buscar as conversas do contato para obter o contact_inbox com source_id
	conversations, err := c.ListContactConversations(contactID)
	if err != nil {
		return "", fmt.Errorf("error getting contact conversations: %w", err)
	}
	
	// Buscar uma conversa ativa no inbox específico
	for _, conv := range conversations {
		if conv.InboxID == inboxID {
			// Buscar detalhes da conversa para obter contact_inbox
			conversationDetails, err := c.GetConversation(conv.ID)
			if err != nil {
				continue // Tenta próxima conversa
			}
			
			if conversationDetails.ContactInbox != nil && conversationDetails.ContactInbox.SourceID != "" {
				// Armazenar no cache (TTL de 30 minutos)
				GlobalCache.SetCachedData(cacheKey, conversationDetails.ContactInbox.SourceID, 1800)
				
				log.Debug().
					Int("contactID", contactID).
					Int("inboxID", inboxID).
					Str("sourceID", conversationDetails.ContactInbox.SourceID).
					Msg("Cached source_id from conversation")
					
				return conversationDetails.ContactInbox.SourceID, nil
			}
		}
	}
	
	// Se não encontrou via conversas, buscar diretamente no contato
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d", c.AccountID, contactID)
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("error getting contact: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return "", fmt.Errorf("Chatwoot API error getting contact: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading contact response: %w", err)
	}

	var contact struct {
		ContactInboxes []struct {
			InboxID  int    `json:"inbox_id"`
			SourceID string `json:"source_id"`
		} `json:"contact_inboxes"`
	}
	
	if err := json.Unmarshal(body, &contact); err != nil {
		return "", fmt.Errorf("error unmarshaling contact response: %w", err)
	}
	
	// Buscar sourceID para o inbox específico
	for _, ci := range contact.ContactInboxes {
		if ci.InboxID == inboxID {
			if ci.SourceID == "" {
				return "", fmt.Errorf("source_id not found for contact %d in inbox %d", contactID, inboxID)
			}
			
			// Armazenar no cache (TTL de 30 minutos)
			GlobalCache.SetCachedData(cacheKey, ci.SourceID, 1800)
			
			log.Debug().
				Int("contactID", contactID).
				Int("inboxID", inboxID).
				Str("sourceID", ci.SourceID).
				Msg("Cached source_id from contact")
				
			return ci.SourceID, nil
		}
	}
	
	return "", fmt.Errorf("contact %d not found in inbox %d", contactID, inboxID)
}

// GetConversation busca detalhes de uma conversa específica
func (c *Client) GetConversation(conversationID int) (*Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d", c.AccountID, conversationID)
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("error getting conversation: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, fmt.Errorf("Chatwoot API error getting conversation: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading conversation response: %w", err)
	}

	var conversation Conversation
	if err := json.Unmarshal(body, &conversation); err != nil {
		return nil, fmt.Errorf("error unmarshaling conversation response: %w", err)
	}
	
	return &conversation, nil
}

// UpdateLastSeenDirect atualiza último acesso usando dados diretos (mais eficiente)
func (c *Client) UpdateLastSeenDirect(conversationID int, inboxID int, sourceID string) error {
	// Buscar inbox_identifier (com cache)
	inboxIdentifier, err := c.getInboxIdentifier(inboxID)
	if err != nil {
		return fmt.Errorf("error getting inbox identifier: %w", err)
	}
	
	// Usar endpoint oficial da chatwoot-lib com dados diretos
	endpoint := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/update_last_seen", 
		inboxIdentifier, sourceID, conversationID)

	log.Debug().
		Int("conversationID", conversationID).
		Int("inboxID", inboxID).
		Str("inboxIdentifier", inboxIdentifier).
		Str("sourceID", sourceID).
		Msg("Updating last seen in Chatwoot conversation with official endpoint (direct)")

	resp, err := c.makeRequest("POST", endpoint, nil)
	if err != nil {
		return fmt.Errorf("error updating last seen: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return fmt.Errorf("Chatwoot API error updating last seen: %w", err)
	}
	
	log.Info().
		Int("conversationID", conversationID).
		Int("inboxID", inboxID).
		Str("inboxIdentifier", inboxIdentifier).
		Str("sourceID", sourceID).
		Msg("Successfully updated last seen in Chatwoot with official endpoint (direct)")

	return nil
}


// GetSourceIDFromContactInboxes busca source_id via endpoint do contato (fallback)
func (c *Client) GetSourceIDFromContactInboxes(contactID int, inboxID int) (string, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d", c.AccountID, contactID)
	
	resp, err := c.makeRequest("GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("error getting contact: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return "", fmt.Errorf("Chatwoot API error getting contact: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading contact response: %w", err)
	}

	// DEBUG: Log do payload completo do contato
	log.Debug().
		Str("endpoint", endpoint).
		Int("contactID", contactID).
		Int("inboxID", inboxID).
		Str("rawContactPayload", string(body)).
		Msg("RAW PAYLOAD from contact API")

	var contact map[string]interface{}
	if err := json.Unmarshal(body, &contact); err != nil {
		return "", fmt.Errorf("error unmarshaling contact response: %w", err)
	}
	
	// DEBUG: Log da estrutura parseada do contato
	log.Debug().
		Interface("parsedContact", contact).
		Msg("PARSED CONTACT from contact API")
	
	// DEBUG: Verificar se contact_inboxes existe
	if contactInboxes, ok := contact["contact_inboxes"]; ok {
		log.Debug().
			Interface("contactInboxes", contactInboxes).
			Msg("FOUND contact_inboxes in contact response")
	} else {
		log.Debug().
			Msg("contact_inboxes NOT FOUND in contact response")
	}
	
	// TODO: Processar dados baseado na estrutura real observada nos logs
	// Por enquanto, retornar erro para analisar o payload
	return "", fmt.Errorf("contact fallback analysis needed - check logs above for payload structure")
}

// MarkMessageAsRead marca mensagens como lidas no Chatwoot
func (c *Client) MarkMessageAsRead(conversationID int, messageIDs []string) error {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages/mark_read", c.AccountID, conversationID)
	
	payload := map[string]interface{}{
		"message_ids": messageIDs,
	}

	log.Debug().
		Int("conversationID", conversationID).
		Strs("messageIDs", messageIDs).
		Msg("Marking messages as read in Chatwoot")

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return fmt.Errorf("error marking messages as read: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return fmt.Errorf("Chatwoot API error marking messages as read: %w", err)
	}
	
	log.Info().
		Int("conversationID", conversationID).
		Strs("messageIDs", messageIDs).
		Msg("Successfully marked messages as read in Chatwoot")

	return nil
}

// DeleteMessage deleta uma mensagem no Chatwoot
func (c *Client) DeleteMessage(conversationID, messageID int) error {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages/%d", c.AccountID, conversationID, messageID)

	log.Info().
		Int("conversationID", conversationID).
		Int("messageID", messageID).
		Str("endpoint", endpoint).
		Msg("Deleting message in Chatwoot")

	resp, err := c.makeRequest("DELETE", endpoint, nil)
	if err != nil {
		return fmt.Errorf("error deleting message: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return fmt.Errorf("Chatwoot API error deleting message: %w", err)
	}

	log.Info().
		Int("conversationID", conversationID).
		Int("messageID", messageID).
		Msg("Successfully deleted message in Chatwoot")

	return nil
}