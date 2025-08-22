package chatwoot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
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
		Bool("has_auth", req.Header.Get("Authorization") != "").
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

	// Tentar parsear erro JSON
	var errorResponse map[string]interface{}
	if err := json.Unmarshal(body, &errorResponse); err == nil {
		if message, ok := errorResponse["message"]; ok {
			return fmt.Errorf("HTTP %d: %v", resp.StatusCode, message)
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

// FindContact busca um contato pelo número de telefone
func (c *Client) FindContact(phone string) (*Contact, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/search", c.AccountID)
	
	// Adicionar parâmetro de busca
	baseURL, _ := url.Parse(c.BaseURL)
	baseURL.Path = path.Join(baseURL.Path, endpoint)
	query := baseURL.Query()
	query.Set("q", phone)
	baseURL.RawQuery = query.Encode()

	req, err := http.NewRequest("GET", baseURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating search request: %w", err)
	}

	req.Header.Set("api_access_token", c.Token)
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error executing search request: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var searchResult struct {
		Payload []Contact `json:"payload"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("error decoding search response: %w", err)
	}

	// Buscar contato com número de telefone correspondente
	for _, contact := range searchResult.Payload {
		if contact.PhoneNumber == phone || contact.Identifier == phone+"@s.whatsapp.net" {
			return &contact, nil
		}
	}

	return nil, nil // Não encontrado
}

// CreateContact cria um novo contato no Chatwoot
func (c *Client) CreateContact(phone, name, avatarURL string, inboxID int) (*Contact, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts", c.AccountID)
	
	payload := map[string]interface{}{
		"inbox_id":     inboxID,
		"name":         name,
		"phone_number": phone,
		"identifier":   phone + "@s.whatsapp.net",
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

// FindConversation busca uma conversa ativa para o contato
func (c *Client) FindConversation(contactID, inboxID int) (*Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/contacts/%d/conversations", c.AccountID, contactID)
	
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

	// Buscar conversa ativa no inbox especificado
	for _, conversation := range result.Payload {
		if conversation.InboxID == inboxID && conversation.Status != "resolved" {
			return &conversation, nil
		}
	}

	return nil, nil // Não encontrado
}

// CreateConversation cria uma nova conversa
func (c *Client) CreateConversation(contactID, inboxID int) (*Conversation, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations", c.AccountID)
	
	payload := map[string]interface{}{
		"contact_id": contactID,
		"inbox_id":   inboxID,
	}

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("error creating conversation: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result Conversation
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding create conversation response: %w", err)
	}

	return &result, nil
}

// SendMessage envia uma mensagem para uma conversa
func (c *Client) SendMessage(conversationID int, content string, messageType int, sourceID string) (*Message, error) {
	endpoint := fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", c.AccountID, conversationID)
	
	payload := map[string]interface{}{
		"content":      content,
		"message_type": messageType, // 0=incoming, 1=outgoing
	}

	if sourceID != "" {
		payload["source_id"] = sourceID
	}

	resp, err := c.makeRequest("POST", endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("error sending message: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleError(resp); err != nil {
		return nil, err
	}

	var result Message
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding send message response: %w", err)
	}

	return &result, nil
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
func (c *Client) SendAttachment(conversationID int, content, filename string, fileData []byte, messageType int) (*Message, error) {
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
	if err := writer.WriteField("message_type", strconv.Itoa(messageType)); err != nil {
		return nil, fmt.Errorf("error writing message_type field: %w", err)
	}
	
	// Adicionar arquivo
	part, err := writer.CreateFormFile("attachments[]", filename)
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