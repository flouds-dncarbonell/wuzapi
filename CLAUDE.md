# Integração Chatwoot + Wuzapi - Planejamento de Implementação

## 📋 Visão Geral do Projeto

**Objetivo:** Integrar o Chatwoot com o wuzapi para permitir atendimento via WhatsApp através da interface do Chatwoot.

**Duração Estimada:** 10-14 dias de desenvolvimento

**Arquitetura:** Implementação nativa em Go, aproveitando a infraestrutura existente do wuzapi.

**Status Atual:** ✅ **FASE 1, 2 e 3 CONCLUÍDAS** - Interface web e integração WhatsApp → Chatwoot funcionando

---

## 🚀 **STATUS DE DESENVOLVIMENTO - ATUALIZADO EM 2024-12-21**

### ✅ **IMPLEMENTADO E FUNCIONANDO:**
- **Interface Web Completa** - Dashboard com configuração visual do Chatwoot
- **Interceptação de Eventos** - Mensagens WhatsApp sendo capturadas
- **Processamento Automático** - Criação de contatos/conversas no Chatwoot
- **Mensagens de Texto** - WhatsApp → Chatwoot funcionando
- **API REST Completa** - CRUD de configurações Chatwoot
- **Cache Inteligente** - Otimização de performance
- **Status em Tempo Real** - Monitoramento da integração

### 🎯 **COMO TESTAR AGORA:**
1. **Acessar Dashboard** → Seção "Configuration" → Card "Chatwoot Integration"
2. **Configurar Credenciais** do Chatwoot (URL, Account ID, Token)
3. **Habilitar Integração** e testar conexão
4. **Enviar mensagem de texto** para o WhatsApp
5. **Verificar no Chatwoot** se mensagem/contato/conversa foram criados

### ✅ **CORREÇÕES APLICADAS EM 2025-08-22:**
- **Problema JSON Response**: Corrigido double-encoding nos handlers Chatwoot
- **Modal Config**: Dados salvos no banco agora aparecem corretamente no modal
- **Logs Detalhados**: Adicionados logs completos para debugging (backend + frontend)
- **Status 502**: Resolvido panic causado por type assertion incorreta na função `Respond()`

### 🚧 **PRÓXIMAS ETAPAS:**
- **FASE 4:** Webhook reverso (Chatwoot → WhatsApp)
- **FASE 5:** Processamento completo de mídias (imagem, vídeo, áudio)

---

## 🔍 Contexto e Análise Inicial

### Estrutura Atual do Wuzapi:
- **Linguagem:** Go (Golang)
- **WhatsApp Library:** whatsmeow  
- **Database:** PostgreSQL/SQLite com migrações automáticas
- **Autenticação:** Token-based por usuário
- **Webhooks:** Sistema multi-webhook por usuário (tabela `user_webhooks`)
- **Eventos:** 75+ tipos de eventos WhatsApp capturados
- **Arquitetura:** Modular (handlers, routes, db, clients)

### Sistema de Eventos Existente:
O wuzapi já captura eventos através do `myEventHandler` em `wmiau.go`:
- ✅ **Message** - Mensagens recebidas/enviadas
- ✅ **ReadReceipt** - Confirmações de leitura  
- ✅ **Presence** - Status online/offline
- ✅ **ChatPresence** - Digitando/gravando
- ✅ **Connected/Disconnected** - Status da conexão
- ✅ **HistorySync** - Sincronização de histórico

### Análise do Chatwoot-lib (Referência):
Baseado na análise da pasta `chatwoot-lib/`, identificamos as funcionalidades principais:
- **ChatwootController** - CRUD de configurações
- **ChatwootService** - Lógica de negócio e API calls
- **ChatwootRouter** - Endpoints REST
- **Schemas de validação** - Validação de dados
- **Cliente PostgreSQL** - Para importação de histórico  
- **Import Helper** - Importação de mensagens históricas

### Estratégia de Integração:
**Não vamos portar** o código TypeScript/Node.js. **Vamos implementar** funcionalidades equivalentes nativas em Go para:
- Melhor performance e menor overhead
- Manter consistência arquitetural
- Aproveitar infraestrutura existente
- Controle total sobre funcionalidades

---

## 🎯 Fases de Implementação

### **FASE 1: Estrutura Base e Configuração** ⏱️ 2-3 dias

#### 1.1 Database Schema
- [ ] **Criar migration para tabela `chatwoot_configs`**
  
  **Localização:** Adicionar em `migrations.go` na variável `migrations`
  
  **Schema SQL:**
  ```sql
  CREATE TABLE chatwoot_configs (
      id TEXT PRIMARY KEY,
      user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      enabled BOOLEAN DEFAULT FALSE,
      account_id TEXT NOT NULL,
      token TEXT NOT NULL, 
      url TEXT NOT NULL,
      name_inbox TEXT DEFAULT '',
      sign_msg BOOLEAN DEFAULT FALSE,
      sign_delimiter TEXT DEFAULT '\n',
      reopen_conversation BOOLEAN DEFAULT TRUE,
      conversation_pending BOOLEAN DEFAULT FALSE,
      merge_brazil_contacts BOOLEAN DEFAULT FALSE,
      ignore_jids TEXT DEFAULT '[]', -- JSON array
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
  );
  ```
  
  **Código Go para adicionar:**
  ```go
  {
      ID:    6,
      Name:  "add_chatwoot_support", 
      UpSQL: addChatwootSupportSQL,
  }
  ```

#### 1.2 Structs e Models Go
- [ ] **Criar `chatwoot.go`** com structs principais:
  
  **Localização:** Novo arquivo `chatwoot.go` na raiz do projeto
  
  **Structs necessárias:**
  ```go
  // Configuração do usuário
  type ChatwootConfig struct {
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
  
  // Estruturas da API Chatwoot
  type ChatwootContact struct {
      ID          int    `json:"id"`
      Name        string `json:"name"`
      PhoneNumber string `json:"phone_number"`
      Identifier  string `json:"identifier"`
      AvatarURL   string `json:"avatar_url,omitempty"`
  }
  
  type ChatwootConversation struct {
      ID        int `json:"id"`
      ContactID int `json:"contact_id"`
      InboxID   int `json:"inbox_id"`
      Status    string `json:"status"`
  }
  
  type ChatwootMessage struct {
      ID             int    `json:"id"`
      Content        string `json:"content"`
      MessageType    int    `json:"message_type"` // 0=incoming, 1=outgoing
      ConversationID int    `json:"conversation_id"`
      SenderID       int    `json:"sender_id,omitempty"`
      SourceID       string `json:"source_id,omitempty"`
  }
  
  type ChatwootInbox struct {
      ID   int    `json:"id"`
      Name string `json:"name"`
  }
  ```

#### 1.3 CRUD Handlers
- [ ] **Criar `chatwoot_handlers.go`** com endpoints:
  
  **Localização:** Novo arquivo `chatwoot_handlers.go` na raiz do projeto
  
  **Endpoints implementar:**
  ```go
  // POST /chatwoot/config - Criar/atualizar configuração
  func (s *server) SetChatwootConfig() http.HandlerFunc {
      // 1. Validar dados de entrada
      // 2. Testar conectividade com Chatwoot
      // 3. Salvar configuração no banco
      // 4. Retornar status
  }
  
  // GET /chatwoot/config - Obter configuração atual  
  func (s *server) GetChatwootConfig() http.HandlerFunc {
      // 1. Buscar configuração do usuário
      // 2. Retornar dados (sem token por segurança)
  }
  
  // DELETE /chatwoot/config - Remover configuração
  func (s *server) DeleteChatwootConfig() http.HandlerFunc {
      // 1. Remover configuração do banco
      // 2. Limpar cache se existir
  }
  
  // GET /chatwoot/status - Status da integração
  func (s *server) GetChatwootStatus() http.HandlerFunc {
      // 1. Verificar se configurado
      // 2. Testar conectividade
      // 3. Retornar estatísticas
  }
  ```

#### 1.4 Rotas e Middleware
- [ ] **Adicionar rotas em `routes.go`**
  
  **Localização:** Modificar arquivo `routes.go` existente
  
  **Código adicionar:**
  ```go
  // Adicionar após as rotas de webhook existentes
  s.router.Handle("/chatwoot/config", c.Then(s.SetChatwootConfig())).Methods("POST")
  s.router.Handle("/chatwoot/config", c.Then(s.GetChatwootConfig())).Methods("GET") 
  s.router.Handle("/chatwoot/config", c.Then(s.DeleteChatwootConfig())).Methods("DELETE")
  s.router.Handle("/chatwoot/status", c.Then(s.GetChatwootStatus())).Methods("GET")
  s.router.Handle("/chatwoot/test", c.Then(s.TestChatwootConnection())).Methods("POST")
  ```
  
  **Nota:** Usar middleware `c` existente que já inclui autenticação `authalice`

#### 1.5 Funções Auxiliares
- [ ] **Criar funções helper em `chatwoot.go`:**
  ```go
  // ValidateChatwootConfig valida configuração
  func ValidateChatwootConfig(config ChatwootConfig) error
  
  // GenerateChatwootID gera ID único  
  func GenerateChatwootID() string
  
  // GetChatwootConfigByUserID busca config do usuário
  func GetChatwootConfigByUserID(db *sqlx.DB, userID string) (*ChatwootConfig, error)
  
  // SaveChatwootConfig salva/atualiza configuração
  func SaveChatwootConfig(db *sqlx.DB, config ChatwootConfig) error
  ```

---

### **FASE 2: Cliente Chatwoot Nativo** ⏱️ 3-4 dias

#### 2.1 HTTP Client Base
- [ ] **Criar `chatwoot_client.go`** com estrutura do cliente HTTP
  
  **Localização:** Novo arquivo `chatwoot_client.go` na raiz do projeto
  
  **Estrutura principal:**
  ```go
  type ChatwootClient struct {
      BaseURL   string
      AccountID string
      Token     string
      UserAgent string
      Timeout   time.Duration
      httpClient *http.Client
  }
  
  // NewChatwootClient cria novo cliente
  func NewChatwootClient(config ChatwootConfig) *ChatwootClient {
      return &ChatwootClient{
          BaseURL:   config.URL,
          AccountID: config.AccountID,
          Token:     config.Token,
          UserAgent: "wuzapi-chatwoot/1.0",
          Timeout:   30 * time.Second,
          httpClient: &http.Client{Timeout: 30 * time.Second},
      }
  }
  
  // makeRequest faz requisição HTTP base
  func (c *ChatwootClient) makeRequest(method, endpoint string, body interface{}) (*http.Response, error)
  
  // handleError processa erros da API Chatwoot
  func (c *ChatwootClient) handleError(resp *http.Response) error
  ```

#### 2.2 Operações Core API
- [ ] **Implementar funções principais da API Chatwoot:**
  
  **Localização:** Continuar em `chatwoot_client.go`
  
  **Endpoints da API necessários:**
  ```go
  // === CONTACTS ===
  // GET /api/v1/accounts/{account_id}/contacts/search?q={phone}
  func (c *ChatwootClient) FindContact(phone string) (*ChatwootContact, error) {
      // 1. Fazer request para search API
      // 2. Filtrar por phone number
      // 3. Retornar primeiro resultado ou nil
  }
  
  // POST /api/v1/accounts/{account_id}/contacts
  func (c *ChatwootClient) CreateContact(phone, name, avatarURL string, inboxID int) (*ChatwootContact, error) {
      payload := map[string]interface{}{
          "inbox_id":     inboxID,
          "name":         name,
          "phone_number": phone,
          "avatar_url":   avatarURL,
      }
      // Fazer POST request
  }
  
  // === CONVERSATIONS ===
  // GET /api/v1/accounts/{account_id}/contacts/{contact_id}/conversations
  func (c *ChatwootClient) FindConversation(contactID, inboxID int) (*ChatwootConversation, error) {
      // 1. Buscar conversas do contato
      // 2. Filtrar por inbox_id e status != resolved
      // 3. Retornar conversa ativa ou nil
  }
  
  // POST /api/v1/accounts/{account_id}/conversations  
  func (c *ChatwootClient) CreateConversation(contactID, inboxID int) (*ChatwootConversation, error) {
      payload := map[string]interface{}{
          "contact_id": contactID,
          "inbox_id":   inboxID,
      }
      // Fazer POST request
  }
  
  // === MESSAGES ===
  // POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages
  func (c *ChatwootClient) SendMessage(conversationID int, content string, messageType int, sourceID string) (*ChatwootMessage, error) {
      payload := map[string]interface{}{
          "content":      content,
          "message_type": messageType, // 0=incoming, 1=outgoing
          "source_id":    sourceID,    // WhatsApp message ID
      }
      // Fazer POST request
  }
  
  // POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages (com attachment)
  func (c *ChatwootClient) SendAttachment(conversationID int, content, filename string, fileData []byte, messageType int) (*ChatwootMessage, error) {
      // 1. Criar multipart form data
      // 2. Adicionar arquivo como attachment
      // 3. Fazer POST request
  }
  
  // === INBOXES ===
  // GET /api/v1/accounts/{account_id}/inboxes
  func (c *ChatwootClient) ListInboxes() ([]ChatwootInbox, error)
  
  // POST /api/v1/accounts/{account_id}/inboxes
  func (c *ChatwootClient) CreateInbox(name, webhookURL string) (*ChatwootInbox, error)
  ```

#### 2.3 Cache e Otimizações
- [ ] **Sistema de cache para contatos/conversas:**
  
  **Localização:** Novo arquivo `chatwoot_cache.go` na raiz do projeto
  
  **Implementação usando go-cache:**
  ```go
  import "github.com/patrickmn/go-cache"
  
  type ChatwootCache struct {
      contacts      *cache.Cache // phone -> ChatwootContact
      conversations *cache.Cache // contactID:inboxID -> ChatwootConversation
      inboxes      *cache.Cache // accountID -> []ChatwootInbox
  }
  
  func NewChatwootCache() *ChatwootCache {
      return &ChatwootCache{
          contacts:      cache.New(30*time.Minute, 10*time.Minute),
          conversations: cache.New(30*time.Minute, 10*time.Minute), 
          inboxes:      cache.New(60*time.Minute, 20*time.Minute),
      }
  }
  
  // Cache methods
  func (cc *ChatwootCache) GetContact(phone string) (*ChatwootContact, bool)
  func (cc *ChatwootCache) SetContact(phone string, contact *ChatwootContact)
  func (cc *ChatwootCache) GetConversation(contactID, inboxID int) (*ChatwootConversation, bool)
  func (cc *ChatwootCache) SetConversation(contactID, inboxID int, conv *ChatwootConversation)
  func (cc *ChatwootCache) InvalidateContact(phone string)
  func (cc *ChatwootCache) InvalidateConversation(contactID, inboxID int)
  ```

#### 2.4 Configuração de Inbox
- [ ] **Auto-configuração de inbox:**
  
  **Localização:** Adicionar em `chatwoot_client.go`
  
  **Funcionalidades:**
  ```go
  // SetupInbox configura inbox automaticamente
  func (c *ChatwootClient) SetupInbox(inboxName, webhookURL string) (*ChatwootInbox, error) {
      // 1. Listar inboxes existentes
      inboxes, err := c.ListInboxes()
      if err != nil {
          return nil, err
      }
      
      // 2. Verificar se inbox já existe
      for _, inbox := range inboxes {
          if inbox.Name == inboxName {
              return &inbox, nil
          }
      }
      
      // 3. Criar novo inbox se não existir
      return c.CreateInbox(inboxName, webhookURL)
  }
  
  // TestConnection testa conectividade com Chatwoot
  func (c *ChatwootClient) TestConnection() error {
      // Fazer GET para /api/v1/accounts/{account_id}/inboxes
      // Verificar se retorna status 200
  }
  
  // GetInboxByName busca inbox pelo nome
  func (c *ChatwootClient) GetInboxByName(name string) (*ChatwootInbox, error)
  ```

#### 2.5 Integração com Cache Global
- [ ] **Adicionar cache global do wuzapi:**
  
  **Localização:** Modificar arquivo existente que gerencia cache (provavelmente `main.go`)
  
  **Adicionar variável global:**
  ```go
  // Adicionar junto com outras variáveis globais como userinfocache
  var chatwootCache *ChatwootCache
  
  // Inicializar no main()
  func main() {
      // ... código existente ...
      chatwootCache = NewChatwootCache()
      // ... resto do código ...
  }
  ```

---

### **FASE 3: Integração com Eventos WhatsApp** ⏱️ 3-4 dias

#### 3.1 Interceptação de Eventos
- [ ] **Modificar `myEventHandler` em `wmiau.go`:**
  
  **Localização:** Arquivo existente `wmiau.go`, função `myEventHandler`
  
  **Modificação necessária:**
  ```go
  func (mycli *MyClient) myEventHandler(rawEvt interface{}) {
      txtid := mycli.userID
      postmap := make(map[string]interface{})
      postmap["event"] = rawEvt
      dowebhook := 0
      path := ""
  
      // === CÓDIGO EXISTENTE ===
      switch evt := rawEvt.(type) {
      case *events.Message:
          // ... código existente de processamento ...
          dowebhook = 1
          
          // === NOVO: PROCESSAR PARA CHATWOOT ===
          go processChatwootEvent(mycli, rawEvt, postmap)
          
      case *events.Receipt:
          // ... código existente ...
          dowebhook = 1
          
          // === NOVO: PROCESSAR PARA CHATWOOT ===
          go processChatwootEvent(mycli, rawEvt, postmap)
          
      // ... outros cases existentes ...
      }
  
      // === CÓDIGO EXISTENTE DE WEBHOOK ===
      if dowebhook == 1 {
          sendEventWithWebHook(mycli, postmap, path)
      }
  }
  ```

#### 3.2 Processamento de Mensagens
- [ ] **Criar `chatwoot_processor.go`** para processamento principal:
  
  **Localização:** Novo arquivo `chatwoot_processor.go` na raiz do projeto
  
  **Função principal:**
  ```go
  // processChatwootEvent processa eventos para Chatwoot
  func processChatwootEvent(mycli *MyClient, rawEvt interface{}, postmap map[string]interface{}) {
      // 1. Verificar se Chatwoot está habilitado para este usuário
      config, err := GetChatwootConfigByUserID(mycli.db, mycli.userID)
      if err != nil || config == nil || !config.Enabled {
          return // Chatwoot não configurado ou desabilitado
      }
      
      // 2. Criar cliente Chatwoot
      client := NewChatwootClient(*config)
      
      // 3. Processar evento baseado no tipo
      switch evt := rawEvt.(type) {
      case *events.Message:
          processMessageEvent(client, config, evt)
      case *events.Receipt:
          processReceiptEvent(client, config, evt)
      case *events.Presence:
          processPresenceEvent(client, config, evt)
      }
  }
  
  // processMessageEvent processa mensagens do WhatsApp → Chatwoot
  func processMessageEvent(client *ChatwootClient, config *ChatwootConfig, evt *events.Message) {
      // 1. Extrair dados da mensagem
      phone := evt.Info.Sender.User
      if phone == "" {
          phone = evt.Info.Chat.User // Para grupos
      }
      
      // 2. Verificar se deve ignorar (grupos, etc)
      if shouldIgnoreMessage(config, evt) {
          return
      }
      
      // 3. Encontrar ou criar contato
      contact := findOrCreateContact(client, phone, evt.Info.PushName)
      if contact == nil {
          return
      }
      
      // 4. Encontrar ou criar conversa
      conversation := findOrCreateConversation(client, contact.ID, config)
      if conversation == nil {
          return
      }
      
      // 5. Processar conteúdo da mensagem
      content := extractMessageContent(evt.Message)
      if content == "" && !hasAttachment(evt.Message) {
          return
      }
      
      // 6. Enviar para Chatwoot
      if hasAttachment(evt.Message) {
          processAttachment(client, conversation.ID, evt, content)
      } else {
          sendTextMessage(client, conversation.ID, content, evt.Info.ID)
      }
  }
  ```

#### 3.3 Processamento de Mídias
- [ ] **Suporte a attachments:**
  
  **Localização:** Continuar em `chatwoot_processor.go`
  
  **Funções de mídia:**
  ```go
  // processAttachment processa anexos do WhatsApp
  func processAttachment(client *ChatwootClient, conversationID int, evt *events.Message, caption string) {
      // 1. Detectar tipo de mídia
      var mediaData []byte
      var filename string
      var mimeType string
      
      if evt.Message.ImageMessage != nil {
          mediaData, filename, mimeType = downloadImage(evt)
      } else if evt.Message.VideoMessage != nil {
          mediaData, filename, mimeType = downloadVideo(evt)
      } else if evt.Message.AudioMessage != nil {
          mediaData, filename, mimeType = downloadAudio(evt)
      } else if evt.Message.DocumentMessage != nil {
          mediaData, filename, mimeType = downloadDocument(evt)
      }
      
      if mediaData == nil {
          return
      }
      
      // 2. Enviar para Chatwoot
      client.SendAttachment(conversationID, caption, filename, mediaData, 0) // 0=incoming
  }
  
  // downloadImage baixa imagem do WhatsApp
  func downloadImage(evt *events.Message) ([]byte, string, string) {
      // Reutilizar lógica existente do wuzapi para download
      // Verificar em wmiau.go como é feito o download de mídia
  }
  
  // extractMessageContent extrai texto das mensagens
  func extractMessageContent(msg *waE2E.Message) string {
      if msg.Conversation != nil {
          return *msg.Conversation
      }
      if msg.ExtendedTextMessage != nil {
          return *msg.ExtendedTextMessage.Text
      }
      if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
          return *msg.ImageMessage.Caption
      }
      // ... outros tipos de mensagem ...
      return ""
  }
  ```

#### 3.4 Eventos de Status
- [ ] **Sincronizar status:**
  
  **Localização:** Continuar em `chatwoot_processor.go`
  
  **Funções de status:**
  ```go
  // processReceiptEvent processa confirmações de leitura
  func processReceiptEvent(client *ChatwootClient, config *ChatwootConfig, evt *events.Receipt) {
      if evt.Type != types.ReceiptTypeRead {
          return // Só processar read receipts
      }
      
      // 1. Buscar conversa baseada no chat
      phone := evt.Chat.User
      contact := findContactByPhone(client, phone)
      if contact == nil {
          return
      }
      
      // 2. Marcar mensagens como lidas no Chatwoot
      // (Chatwoot não tem API específica, mas podemos atualizar última atividade)
      updateLastActivity(client, contact.ID)
  }
  
  // processPresenceEvent processa status de presença
  func processPresenceEvent(client *ChatwootClient, config *ChatwootConfig, evt *events.Presence) {
      phone := evt.From.User
      
      // Atualizar cache local com status de presença
      // Chatwoot não tem API específica para presença, mas podemos usar para analytics
      updatePresenceCache(phone, !evt.Unavailable)
  }
  
  // Funções auxiliares
  func shouldIgnoreMessage(config *ChatwootConfig, evt *events.Message) bool {
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
      
      return false
  }
  
  func findOrCreateContact(client *ChatwootClient, phone, name string) *ChatwootContact {
      // 1. Verificar cache
      if contact, found := chatwootCache.GetContact(phone); found {
          return contact
      }
      
      // 2. Buscar no Chatwoot
      contact, err := client.FindContact(phone)
      if err == nil && contact != nil {
          chatwootCache.SetContact(phone, contact)
          return contact
      }
      
      // 3. Criar novo contato
      if name == "" {
          name = phone
      }
      contact, err = client.CreateContact(phone, name, "", getInboxID(client))
      if err != nil {
          return nil
      }
      
      chatwootCache.SetContact(phone, contact)
      return contact
  }
  ```

#### 3.5 Integração com Sistema Existente
- [ ] **Reaproveitar código do wuzapi:**
  
  **Localização:** Reutilizar funções existentes de `wmiau.go`
  
  **Funções a reutilizar:**
  ```go
  // Reutilizar função de download de mídia existente
  func reuseExistingDownload(evt *events.Message) ([]byte, string, error) {
      // Localizar em wmiau.go a função que faz download
      // Exemplo: downloadMediaMessage() ou similar
  }
  
  // Reutilizar processamento S3 se configurado
  func reuseS3Upload(data []byte, filename string) (string, error) {
      // Se S3 estiver configurado, usar para armazenar mídia
      // Depois enviar URL para Chatwoot em vez do arquivo
  }
  ```

---

### **FASE 4: Webhook Reverso (Chatwoot → WhatsApp)** ⏱️ 2-3 dias

#### 4.1 Endpoint Webhook
- [ ] **Criar rota `/chatwoot/webhook/{userID}`:**
  
  **Localização:** Adicionar em `routes.go` e criar handler em `chatwoot_webhook.go`
  
  **Rota adicionar:**
  ```go
  // Adicionar em routes.go - SEM middleware de auth (Chatwoot vai chamar)
  s.router.Handle("/chatwoot/webhook/{userID}", s.ChatwootWebhook()).Methods("POST")
  ```
  
  **Handler principal:**
  ```go
  // Arquivo: chatwoot_webhook.go
  func (s *server) ChatwootWebhook() http.HandlerFunc {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          // 1. Extrair userID da URL
          vars := mux.Vars(r)
          userID := vars["userID"]
          
          // 2. Verificar se usuário existe e tem Chatwoot configurado
          config, err := GetChatwootConfigByUserID(s.db, userID)
          if err != nil || config == nil || !config.Enabled {
              http.Error(w, "Chatwoot not configured", http.StatusNotFound)
              return
          }
          
          // 3. Parse do payload JSON
          var payload ChatwootWebhookPayload
          if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
              http.Error(w, "Invalid JSON", http.StatusBadRequest)
              return
          }
          
          // 4. Processar webhook
          err = processChatwootWebhook(s, userID, config, &payload)
          if err != nil {
              log.Error().Err(err).Str("userID", userID).Msg("Error processing Chatwoot webhook")
              http.Error(w, "Processing error", http.StatusInternalServerError)
              return
          }
          
          // 5. Resposta de sucesso
          w.WriteHeader(http.StatusOK)
          json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
      })
  }
  ```

#### 4.2 Processamento de Mensagens Chatwoot
- [ ] **Implementar estruturas de payload e processamento:**
  
  **Localização:** Continuar em `chatwoot_webhook.go`
  
  **Estruturas do payload:**
  ```go
  type ChatwootWebhookPayload struct {
      Event        string `json:"event"`
      MessageType  string `json:"message_type"`
      Private      bool   `json:"private"`
      Content      string `json:"content"`
      Conversation struct {
          ID     int `json:"id"`
          Status string `json:"status"`
          Meta   struct {
              Sender struct {
                  Identifier  string `json:"identifier"`
                  PhoneNumber string `json:"phone_number"`
              } `json:"sender"`
          } `json:"meta"`
          ContactInbox struct {
              SourceID string `json:"source_id"`
          } `json:"contact_inbox"`
      } `json:"conversation"`
      Inbox struct {
          ID   int    `json:"id"`
          Name string `json:"name"`
      } `json:"inbox"`
      Sender struct {
          ID   int    `json:"id"`
          Name string `json:"name"`
          Type string `json:"type"`
      } `json:"sender"`
      Attachments []struct {
          DataURL  string `json:"data_url"`
          FileType string `json:"file_type"`
          FileName string `json:"file_name"`
      } `json:"attachments"`
  }
  
  // processChatwootWebhook processa webhook do Chatwoot
  func processChatwootWebhook(s *server, userID string, config *ChatwootConfig, payload *ChatwootWebhookPayload) error {
      // 1. Verificar se é evento de mensagem
      if payload.Event != "message_created" && payload.Event != "message_updated" {
          return nil // Ignorar outros eventos
      }
      
      // 2. Verificar se é mensagem outgoing (do agente)
      if payload.MessageType != "outgoing" {
          return nil // Só processar mensagens de saída
      }
      
      // 3. Verificar se é mensagem privada (nota interna)
      if payload.Private {
          return nil // Ignorar mensagens privadas
      }
      
      // 4. Obter cliente WhatsApp do usuário
      client := clientManager.GetWhatsmeowClient(userID)
      if client == nil {
          return fmt.Errorf("WhatsApp client not found for user %s", userID)
      }
      
      // 5. Extrair número do telefone
      phone := extractPhoneFromPayload(payload)
      if phone == "" {
          return fmt.Errorf("could not extract phone number from payload")
      }
      
      // 6. Enviar mensagem via WhatsApp
      return sendWhatsAppMessage(client, phone, payload)
  }
  ```

#### 4.3 Envio de Mensagens WhatsApp
- [ ] **Implementar envio de mensagens:**
  
  **Localização:** Continuar em `chatwoot_webhook.go`
  
  **Funções de envio:**
  ```go
  // sendWhatsAppMessage envia mensagem para WhatsApp
  func sendWhatsAppMessage(client *whatsmeow.Client, phone string, payload *ChatwootWebhookPayload) error {
      // 1. Construir JID do destinatário
      jid, err := types.ParseJID(phone + "@s.whatsapp.net")
      if err != nil {
          return fmt.Errorf("invalid phone number: %s", phone)
      }
      
      // 2. Verificar se há anexos
      if len(payload.Attachments) > 0 {
          return sendWhatsAppAttachment(client, jid, payload)
      }
      
      // 3. Enviar mensagem de texto
      if payload.Content != "" {
          return sendWhatsAppText(client, jid, payload.Content)
      }
      
      return nil
  }
  
  // sendWhatsAppText envia texto para WhatsApp
  func sendWhatsAppText(client *whatsmeow.Client, jid types.JID, content string) error {
      msg := &waE2E.Message{
          Conversation: proto.String(content),
      }
      
      _, err := client.SendMessage(context.Background(), jid, msg)
      return err
  }
  
  // sendWhatsAppAttachment envia anexo para WhatsApp  
  func sendWhatsAppAttachment(client *whatsmeow.Client, jid types.JID, payload *ChatwootWebhookPayload) error {
      for _, attachment := range payload.Attachments {
          // 1. Download do arquivo do Chatwoot
          fileData, err := downloadChatwootAttachment(attachment.DataURL)
          if err != nil {
              continue
          }
          
          // 2. Determinar tipo de mídia
          switch attachment.FileType {
          case "image":
              err = sendWhatsAppImage(client, jid, fileData, payload.Content)
          case "video":
              err = sendWhatsAppVideo(client, jid, fileData, payload.Content)
          case "audio":
              err = sendWhatsAppAudio(client, jid, fileData)
          default:
              err = sendWhatsAppDocument(client, jid, fileData, attachment.FileName, payload.Content)
          }
          
          if err != nil {
              return err
          }
      }
      return nil
  }
  
  // downloadChatwootAttachment baixa anexo do Chatwoot
  func downloadChatwootAttachment(dataURL string) ([]byte, error) {
      resp, err := http.Get(dataURL)
      if err != nil {
          return nil, err
      }
      defer resp.Body.Close()
      
      return io.ReadAll(resp.Body)
  }
  ```

#### 4.4 Gestão de Conversas e Estados
- [ ] **Controle de estado das conversas:**
  
  **Localização:** Continuar em `chatwoot_webhook.go`
  
  **Funções de controle:**
  ```go
  // extractPhoneFromPayload extrai número de telefone do payload
  func extractPhoneFromPayload(payload *ChatwootWebhookPayload) string {
      // 1. Tentar pegar do identifier (formato: 5511999999999@s.whatsapp.net)
      identifier := payload.Conversation.Meta.Sender.Identifier
      if identifier != "" && strings.Contains(identifier, "@") {
          return strings.Split(identifier, "@")[0]
      }
      
      // 2. Tentar pegar do phone_number (formato: +5511999999999)
      phone := payload.Conversation.Meta.Sender.PhoneNumber
      if phone != "" {
          return strings.TrimPrefix(phone, "+")
      }
      
      return ""
  }
  
  // processConversationStatus processa mudanças de status da conversa
  func processConversationStatus(payload *ChatwootWebhookPayload) {
      // Se conversa foi resolvida, poderiam fazer alguma ação
      // Por exemplo, enviar mensagem de encerramento
      if payload.Conversation.Status == "resolved" {
          // Log ou ação específica
      }
  }
  
  // reutilizar funções existentes do wuzapi para envio
  func sendWhatsAppImage(client *whatsmeow.Client, jid types.JID, data []byte, caption string) error {
      // Reutilizar lógica de sendImage do handlers.go existente
      // Adaptar para receber dados binários em vez de base64
  }
  
  func sendWhatsAppVideo(client *whatsmeow.Client, jid types.JID, data []byte, caption string) error {
      // Reutilizar lógica de sendVideo do handlers.go existente
  }
  
  func sendWhatsAppAudio(client *whatsmeow.Client, jid types.JID, data []byte) error {
      // Reutilizar lógica de sendAudio do handlers.go existente
  }
  
  func sendWhatsAppDocument(client *whatsmeow.Client, jid types.JID, data []byte, filename, caption string) error {
      // Reutilizar lógica de sendDocument do handlers.go existente
  }
  ```

#### 4.5 Configuração do Webhook no Chatwoot
- [ ] **Auto-configuração do webhook:**
  
  **Localização:** Adicionar em `chatwoot_client.go`
  
  **Função de configuração:**
  ```go
  // setupWebhookURL configura URL do webhook no inbox do Chatwoot
  func (c *ChatwootClient) setupWebhookURL(inboxID int, webhookURL string) error {
      // PUT /api/v1/accounts/{account_id}/inboxes/{inbox_id}
      payload := map[string]interface{}{
          "channel": map[string]interface{}{
              "webhook_url": webhookURL,
          },
      }
      
      endpoint := fmt.Sprintf("/api/v1/accounts/%s/inboxes/%d", c.AccountID, inboxID)
      resp, err := c.makeRequest("PUT", endpoint, payload)
      if err != nil {
          return err
      }
      defer resp.Body.Close()
      
      if resp.StatusCode != 200 {
          return c.handleError(resp)
      }
      
      return nil
  }
  ```

---

### **FASE 5: Interface e Configurações** ⏱️ 2-3 dias

#### 5.1 Dashboard Web
- [ ] **Adicionar seção Chatwoot ao dashboard:**
  
  **Localização:** Modificar `static/dashboard/index.html` e `static/dashboard/js/app.js`
  
  **HTML adicionar:**
  ```html
  <!-- Adicionar nova seção no dashboard existente -->
  <div class="panel" id="chatwoot-panel" style="display: none;">
      <h2>Integração Chatwoot</h2>
      
      <!-- Status da conexão -->
      <div class="status-section">
          <h3>Status</h3>
          <div id="chatwoot-status" class="status-badge">
              <span id="status-text">Não configurado</span>
              <span id="status-indicator" class="indicator"></span>
          </div>
      </div>
      
      <!-- Formulário de configuração -->
      <div class="config-section">
          <h3>Configuração</h3>
          <form id="chatwoot-config-form">
              <div class="form-group">
                  <label for="chatwoot-enabled">Habilitar Chatwoot:</label>
                  <input type="checkbox" id="chatwoot-enabled" name="enabled">
              </div>
              
              <div class="form-group">
                  <label for="chatwoot-url">URL do Chatwoot:</label>
                  <input type="url" id="chatwoot-url" name="url" placeholder="https://app.chatwoot.com">
              </div>
              
              <div class="form-group">
                  <label for="chatwoot-account-id">Account ID:</label>
                  <input type="text" id="chatwoot-account-id" name="account_id" placeholder="123">
              </div>
              
              <div class="form-group">
                  <label for="chatwoot-token">Token:</label>
                  <input type="password" id="chatwoot-token" name="token" placeholder="Token da API">
              </div>
              
              <div class="form-group">
                  <label for="chatwoot-inbox-name">Nome do Inbox:</label>
                  <input type="text" id="chatwoot-inbox-name" name="name_inbox" placeholder="WhatsApp Bot">
              </div>
              
              <!-- Configurações avançadas -->
              <details class="advanced-config">
                  <summary>Configurações Avançadas</summary>
                  
                  <div class="form-group">
                      <label for="chatwoot-sign-msg">Assinar mensagens:</label>
                      <input type="checkbox" id="chatwoot-sign-msg" name="sign_msg">
                  </div>
                  
                  <div class="form-group">
                      <label for="chatwoot-reopen">Reabrir conversas:</label>
                      <input type="checkbox" id="chatwoot-reopen" name="reopen_conversation" checked>
                  </div>
                  
                  <div class="form-group">
                      <label for="chatwoot-pending">Conversas pendentes:</label>
                      <input type="checkbox" id="chatwoot-pending" name="conversation_pending">
                  </div>
              </details>
              
              <div class="form-actions">
                  <button type="button" id="test-connection">Testar Conexão</button>
                  <button type="submit">Salvar Configuração</button>
              </div>
          </form>
      </div>
      
      <!-- Estatísticas -->
      <div class="stats-section">
          <h3>Estatísticas</h3>
          <div class="stats-grid">
              <div class="stat-card">
                  <span class="stat-label">Mensagens Enviadas</span>
                  <span class="stat-value" id="messages-sent">0</span>
              </div>
              <div class="stat-card">
                  <span class="stat-label">Conversas Ativas</span>
                  <span class="stat-value" id="active-conversations">0</span>
              </div>
              <div class="stat-card">
                  <span class="stat-label">Última Sincronização</span>
                  <span class="stat-value" id="last-sync">Nunca</span>
              </div>
          </div>
      </div>
      
      <!-- Logs -->
      <div class="logs-section">
          <h3>Logs Recentes</h3>
          <div id="chatwoot-logs" class="logs-container">
              <!-- Logs serão inseridos aqui via JavaScript -->
          </div>
      </div>
  </div>
  ```

#### 5.2 JavaScript do Dashboard
- [ ] **Implementar funcionalidades JavaScript:**
  
  **Localização:** Modificar `static/dashboard/js/app.js`
  
  **JavaScript adicionar:**
  ```javascript
  // === CHATWOOT FUNCTIONS ===
  
  // Inicializar seção Chatwoot
  function initChatwoot() {
      // Adicionar botão Chatwoot ao menu
      addChatwootMenuItem();
      
      // Carregar configuração atual
      loadChatwootConfig();
      
      // Configurar event listeners
      setupChatwootEventListeners();
      
      // Atualizar status periodicamente
      setInterval(updateChatwootStatus, 30000); // 30 segundos
  }
  
  // Adicionar item ao menu
  function addChatwootMenuItem() {
      const menu = document.querySelector('.menu');
      const chatwootItem = document.createElement('li');
      chatwootItem.innerHTML = '<a href="#" onclick="showChatwoot()">Chatwoot</a>';
      menu.appendChild(chatwootItem);
  }
  
  // Mostrar painel Chatwoot
  function showChatwoot() {
      hideAllPanels();
      document.getElementById('chatwoot-panel').style.display = 'block';
      loadChatwootConfig();
  }
  
  // Carregar configuração atual
  async function loadChatwootConfig() {
      try {
          const response = await fetch('/chatwoot/config', {
              headers: { 'token': getToken() }
          });
          
          if (response.ok) {
              const config = await response.json();
              populateChatwootForm(config);
              updateChatwootStatus();
          }
      } catch (error) {
          console.error('Error loading Chatwoot config:', error);
      }
  }
  
  // Preencher formulário com dados
  function populateChatwootForm(config) {
      document.getElementById('chatwoot-enabled').checked = config.enabled || false;
      document.getElementById('chatwoot-url').value = config.url || '';
      document.getElementById('chatwoot-account-id').value = config.account_id || '';
      document.getElementById('chatwoot-token').value = ''; // Não mostrar token por segurança
      document.getElementById('chatwoot-inbox-name').value = config.name_inbox || '';
      document.getElementById('chatwoot-sign-msg').checked = config.sign_msg || false;
      document.getElementById('chatwoot-reopen').checked = config.reopen_conversation !== false;
      document.getElementById('chatwoot-pending').checked = config.conversation_pending || false;
  }
  
  // Configurar event listeners
  function setupChatwootEventListeners() {
      // Formulário de configuração
      document.getElementById('chatwoot-config-form').addEventListener('submit', saveChatwootConfig);
      
      // Botão de teste
      document.getElementById('test-connection').addEventListener('click', testChatwootConnection);
  }
  
  // Salvar configuração
  async function saveChatwootConfig(event) {
      event.preventDefault();
      
      const formData = new FormData(event.target);
      const config = Object.fromEntries(formData.entries());
      
      // Converter checkboxes para boolean
      config.enabled = config.enabled === 'on';
      config.sign_msg = config.sign_msg === 'on';
      config.reopen_conversation = config.reopen_conversation === 'on';
      config.conversation_pending = config.conversation_pending === 'on';
      
      try {
          const response = await fetch('/chatwoot/config', {
              method: 'POST',
              headers: {
                  'Content-Type': 'application/json',
                  'token': getToken()
              },
              body: JSON.stringify(config)
          });
          
          if (response.ok) {
              showNotification('Configuração salva com sucesso!', 'success');
              loadChatwootConfig(); // Recarregar para atualizar status
          } else {
              const error = await response.text();
              showNotification('Erro ao salvar: ' + error, 'error');
          }
      } catch (error) {
          showNotification('Erro de conexão: ' + error.message, 'error');
      }
  }
  
  // Testar conexão
  async function testChatwootConnection() {
      const button = document.getElementById('test-connection');
      button.disabled = true;
      button.textContent = 'Testando...';
      
      try {
          const response = await fetch('/chatwoot/test', {
              method: 'POST',
              headers: { 'token': getToken() }
          });
          
          if (response.ok) {
              showNotification('Conexão testada com sucesso!', 'success');
          } else {
              const error = await response.text();
              showNotification('Erro na conexão: ' + error, 'error');
          }
      } catch (error) {
          showNotification('Erro de rede: ' + error.message, 'error');
      } finally {
          button.disabled = false;
          button.textContent = 'Testar Conexão';
      }
  }
  
  // Atualizar status
  async function updateChatwootStatus() {
      try {
          const response = await fetch('/chatwoot/status', {
              headers: { 'token': getToken() }
          });
          
          if (response.ok) {
              const status = await response.json();
              updateStatusDisplay(status);
              updateStats(status);
          }
      } catch (error) {
          updateStatusDisplay({ connected: false, error: error.message });
      }
  }
  
  // Atualizar display de status
  function updateStatusDisplay(status) {
      const statusText = document.getElementById('status-text');
      const statusIndicator = document.getElementById('status-indicator');
      
      if (status.connected) {
          statusText.textContent = 'Conectado';
          statusIndicator.className = 'indicator green';
      } else if (status.configured) {
          statusText.textContent = 'Configurado (Desconectado)';
          statusIndicator.className = 'indicator yellow';
      } else {
          statusText.textContent = 'Não configurado';
          statusIndicator.className = 'indicator red';
      }
  }
  
  // Atualizar estatísticas
  function updateStats(status) {
      if (status.stats) {
          document.getElementById('messages-sent').textContent = status.stats.messages_sent || 0;
          document.getElementById('active-conversations').textContent = status.stats.active_conversations || 0;
          document.getElementById('last-sync').textContent = status.stats.last_sync || 'Nunca';
      }
  }
  
  // Inicializar quando DOM estiver pronto
  document.addEventListener('DOMContentLoaded', function() {
      // ... código existente ...
      initChatwoot();
  });
  ```

#### 5.3 CSS para Interface
- [ ] **Estilos CSS para seção Chatwoot:**
  
  **Localização:** Adicionar em `static/dashboard/css/app.css`
  
  **CSS adicionar:**
  ```css
  /* === CHATWOOT STYLES === */
  
  .status-badge {
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 10px;
      background: #f5f5f5;
      border-radius: 5px;
      margin-bottom: 20px;
  }
  
  .indicator {
      width: 12px;
      height: 12px;
      border-radius: 50%;
      display: inline-block;
  }
  
  .indicator.green { background-color: #4caf50; }
  .indicator.yellow { background-color: #ff9800; }
  .indicator.red { background-color: #f44336; }
  
  .advanced-config {
      margin: 20px 0;
      border: 1px solid #ddd;
      border-radius: 5px;
      padding: 10px;
  }
  
  .advanced-config summary {
      cursor: pointer;
      font-weight: bold;
      padding: 5px;
  }
  
  .form-actions {
      display: flex;
      gap: 10px;
      margin-top: 20px;
  }
  
  .stats-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 15px;
      margin: 20px 0;
  }
  
  .stat-card {
      padding: 15px;
      background: #f8f9fa;
      border-radius: 5px;
      text-align: center;
      border: 1px solid #e9ecef;
  }
  
  .stat-label {
      display: block;
      font-size: 0.9em;
      color: #6c757d;
      margin-bottom: 5px;
  }
  
  .stat-value {
      display: block;
      font-size: 1.5em;
      font-weight: bold;
      color: #495057;
  }
  
  .logs-container {
      max-height: 300px;
      overflow-y: auto;
      background: #f8f9fa;
      border: 1px solid #dee2e6;
      border-radius: 5px;
      padding: 10px;
      font-family: monospace;
      font-size: 0.9em;
  }
  
  .log-entry {
      margin-bottom: 5px;
      padding: 2px 0;
  }
  
  .log-entry.error { color: #dc3545; }
  .log-entry.warning { color: #ffc107; }
  .log-entry.info { color: #17a2b8; }
  .log-entry.success { color: #28a745; }
  
  .notification {
      position: fixed;
      top: 20px;
      right: 20px;
      padding: 15px 20px;
      border-radius: 5px;
      color: white;
      font-weight: bold;
      z-index: 1000;
      animation: slideIn 0.3s ease-out;
  }
  
  .notification.success { background-color: #28a745; }
  .notification.error { background-color: #dc3545; }
  
  @keyframes slideIn {
      from { transform: translateX(100%); }
      to { transform: translateX(0); }
  }
  
  /* Responsive adjustments */
  @media (max-width: 768px) {
      .stats-grid {
          grid-template-columns: 1fr;
      }
      
      .form-actions {
          flex-direction: column;
      }
  }
  ```

#### 5.4 Endpoint de Estatísticas
- [ ] **Implementar endpoint de estatísticas:**
  
  **Localização:** Adicionar em `chatwoot_handlers.go`
  
  **Handler adicionar:**
  ```go
  // GetChatwootStatus retorna status e estatísticas da integração
  func (s *server) GetChatwootStatus() http.HandlerFunc {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          userinfo := r.Context().Value("userinfo").(Values)
          userID := userinfo.Get("Id")
          
          // Buscar configuração
          config, err := GetChatwootConfigByUserID(s.db, userID)
          if err != nil {
              s.Respond(w, r, http.StatusInternalServerError, err)
              return
          }
          
          status := map[string]interface{}{
              "configured": config != nil,
              "connected":  false,
              "stats": map[string]interface{}{
                  "messages_sent":        0,
                  "active_conversations": 0,
                  "last_sync":           "Nunca",
              },
          }
          
          if config != nil && config.Enabled {
              // Testar conectividade
              client := NewChatwootClient(*config)
              if err := client.TestConnection(); err == nil {
                  status["connected"] = true
              }
              
              // Buscar estatísticas (implementar conforme necessidade)
              stats := getChatwootStats(s.db, userID)
              status["stats"] = stats
          }
          
          s.Respond(w, r, http.StatusOK, status)
      })
  }
  
  // getChatwootStats busca estatísticas do banco
  func getChatwootStats(db *sqlx.DB, userID string) map[string]interface{} {
      // Implementar queries para estatísticas
      // Por exemplo: contar mensagens enviadas hoje, conversas ativas, etc.
      return map[string]interface{}{
          "messages_sent":        0,
          "active_conversations": 0,
          "last_sync":           "Nunca",
      }
  }
  ```

---

## 🛠️ Arquivos a Serem Criados/Modificados

### 📁 Estrutura Atual (Reorganizada):
```
wuzapi-dev/
├── chatwoot/                      # 📁 Package organizado
│   ├── chatwoot.go               # Entry point e wrappers
│   ├── models.go                 # Structs e tipos principais  
│   ├── client.go                 # Cliente HTTP para API Chatwoot
│   ├── handlers.go               # Endpoints REST (CRUD config)
│   └── cache.go                  # Sistema de cache para contatos/conversas
├── chatwoot_processor.go         # 🚧 Processamento eventos WhatsApp → Chatwoot (PENDENTE)
└── chatwoot_webhook.go           # 🚧 Webhook reverso (Chatwoot → WhatsApp) (PENDENTE)
```

### 📝 Arquivos Modificados:
```
wuzapi-dev/
├── migrations.go                  # ✅ Nova migration para chatwoot_configs
├── routes.go                      # ✅ Rotas /chatwoot/* integradas
├── main.go                        # ✅ Cache e wrapper inicializados
├── wmiau.go                       # 🚧 Chamar processChatwootEvent() (PENDENTE)
└── static/dashboard/              # 🚧 Interface web (PENDENTE)
    ├── index.html                 # 🚧 Seção Chatwoot
    ├── js/app.js                  # 🚧 Funções JavaScript Chatwoot
    └── css/app.css                # 🚧 Estilos CSS Chatwoot
```

### 🔧 Pontos de Integração Específicos:

#### Em `wmiau.go` - linha ~418 (função myEventHandler):
```go
// ANTES:
case *events.Message:
    // ... código existente ...
    dowebhook = 1

// DEPOIS:
case *events.Message:
    // ... código existente ...
    dowebhook = 1
    
    // NOVO: Processar para Chatwoot
    go processChatwootEvent(mycli, rawEvt, postmap)
```

#### Em `routes.go` - linha ~140 (após rotas existentes):
```go
// NOVO: Adicionar após rotas de webhook existentes
s.router.Handle("/chatwoot/config", c.Then(s.SetChatwootConfig())).Methods("POST")
s.router.Handle("/chatwoot/config", c.Then(s.GetChatwootConfig())).Methods("GET")
s.router.Handle("/chatwoot/config", c.Then(s.DeleteChatwootConfig())).Methods("DELETE")
s.router.Handle("/chatwoot/status", c.Then(s.GetChatwootStatus())).Methods("GET")
s.router.Handle("/chatwoot/test", c.Then(s.TestChatwootConnection())).Methods("POST")
s.router.Handle("/chatwoot/webhook/{userID}", s.ChatwootWebhook()).Methods("POST")
```

#### Em `migrations.go` - linha ~58 (array migrations):
```go
// NOVO: Adicionar nova migration
{
    ID:    6,
    Name:  "add_chatwoot_support",
    UpSQL: addChatwootSupportSQL,
}
```

#### Em `main.go` - após linha ~96 (após userinfocache):
```go
// NOVO: Inicializar cache Chatwoot
var chatwootCache *ChatwootCache

func main() {
    // ... código existente ...
    userinfocache = cache.New(cache.NoExpiration, cache.NoExpiration)
    
    // NOVO: Inicializar cache Chatwoot
    chatwootCache = NewChatwootCache()
    
    // ... resto do código ...
}
```

---

## 🔄 Fluxo de Dados Detalhado

### 📥 WhatsApp → Chatwoot (Mensagem Recebida):
```mermaid
WhatsApp Message Event
    ↓
myEventHandler() [wmiau.go:418]
    ↓
processChatwootEvent() [chatwoot_processor.go]
    ↓
GetChatwootConfigByUserID() - Verificar se habilitado
    ↓
findOrCreateContact() - Buscar/criar contato
    ↓
findOrCreateConversation() - Buscar/criar conversa
    ↓
Chatwoot API [POST /messages] - Enviar mensagem
    ↓
Cache Update - Atualizar cache local
    ↓
Response/Log - Confirmar processamento
```

### 📤 Chatwoot → WhatsApp (Resposta do Agente):
```mermaid
Chatwoot Webhook Event
    ↓
ChatwootWebhook() [chatwoot_webhook.go]
    ↓
Validate Payload - Verificar evento e autenticação
    ↓
Extract Phone Number - Extrair número do destinatário
    ↓
Get WhatsApp Client - Obter cliente do usuário
    ↓
sendWhatsAppMessage() - Enviar via whatsmeow
    ↓
WhatsApp API - Entregar mensagem
    ↓
Delivery Confirmation - Confirmar entrega
```

### 🔄 Ciclo Completo de Conversa:
```
1. Cliente envia: "Olá" via WhatsApp
2. wuzapi → processChatwootEvent() → Cria contato/conversa no Chatwoot
3. Agente responde: "Como posso ajudar?" no Chatwoot  
4. Chatwoot → webhook → wuzapi → sendWhatsAppMessage()
5. Cliente recebe resposta no WhatsApp
```

---

## 📊 Status de Implementação

### ✅ **FASE 1: Estrutura Base e Configuração** - **CONCLUÍDA**
- [x] **Database Schema** - Migration para `chatwoot_configs` ✅ 
- [x] **Structs e Models** - Package `chatwoot` com tipos limpos ✅
- [x] **CRUD Handlers** - Endpoints REST organizados ✅
- [x] **Rotas** - Integração no sistema de rotas ✅
- [x] **Cliente HTTP** - API Chatwoot client completo ✅
- [x] **Sistema de Cache** - Cache otimizado com TTL ✅
- [x] **Organização** - Código em pasta `chatwoot/` ✅

### 🔄 **FASE 2: Cliente Chatwoot Nativo** - **CONCLUÍDA**
- [x] **HTTP Client Base** - Cliente HTTP robusto ✅
- [x] **Operações Core API** - CRUD completo (contacts, conversations, messages) ✅
- [x] **Cache e Otimizações** - Sistema de cache com `go-cache` ✅
- [x] **Configuração de Inbox** - Auto-setup de inboxes ✅
- [x] **Integração Global** - Cache e wrappers no main ✅

### ✅ **FASE 3: Integração com Eventos WhatsApp** - **CONCLUÍDA**
- [x] **Interceptação de Eventos** - Modificar `myEventHandler` ✅
- [x] **Processamento de Mensagens** - WhatsApp → Chatwoot ✅
- [x] **Eventos de Status** - Sincronizar status ✅
- [x] **Integração Sistema** - Package `chatwoot/processor.go` ✅
- [ ] **Processamento de Mídias** - Suporte a attachments (TODO FASE 4)

### ✅ **FASE 5: Interface Web** - **CONCLUÍDA**
- [x] **Dashboard** - Seção Chatwoot no painel ✅
- [x] **JavaScript** - Funções de configuração ✅
- [x] **CSS** - Estilos da interface ✅
- [x] **Modal Completo** - Configuração visual avançada ✅
- [x] **Status em Tempo Real** - Indicadores visuais ✅

### 🚧 **FASE 4: Webhook Reverso** - **PENDENTE**
- [ ] **Endpoint Webhook** - `/chatwoot/webhook/{userID}`
- [ ] **Processamento Mensagens** - Chatwoot → WhatsApp
- [ ] **Envio WhatsApp** - Integração com whatsmeow
- [ ] **Gestão Estados** - Controle de conversas
- [ ] **Auto-configuração** - Setup automático webhook
- [ ] **Processamento Mídias Completo** - Imagem, vídeo, áudio, documento

---

## ✅ Critérios de Aceitação

### 🎯 Funcionalidades Mínimas (MVP):
- [x] **Configuração via API** - Endpoints REST funcionando ✅
- [x] **Configuração via Interface** - Dashboard web completo ✅
- [x] **Mensagens de Texto** - WhatsApp → Chatwoot funcionando ✅
- [x] **Criação Automática** - Contatos e conversas criados automaticamente ✅
- [x] **Cache Funcional** - Sistema de cache operacional ✅
- [x] **Tratamento de Erros** - Logs e fallbacks implementados ✅
- [ ] **Sincronização Bidirecional** - Chatwoot → WhatsApp (FASE 4)

### 🌟 Funcionalidades Avançadas:
- [ ] **Suporte a Mídias** - Imagem, áudio, vídeo, documento
- [ ] **Status de Leitura** - Sincronização de read receipts
- [ ] **Configurações Avançadas** - Filtros, assinatura, etc.
- [ ] **Estatísticas Dashboard** - Métricas em tempo real
- [ ] **Suporte a Grupos** - Mensagens de grupo (opcional)

### 🔒 Critérios de Qualidade:
- [ ] **Testes Funcionais** - Cenários principais testados
- [ ] **Performance** - < 500ms para processar mensagem
- [ ] **Confiabilidade** - > 99% de entrega sem falhas
- [ ] **Documentação** - README com instruções de configuração

---

## 🚀 Comandos de Desenvolvimento e Testes

### 🔧 Build e Execução:
```bash
# Build local
go build -o wuzapi .

# Executar com logs detalhados
./wuzapi -logtype json

# Build para produção
go build -ldflags "-s -w" -o wuzapi .
```

### 🧪 Testes da API:
```bash
# 1. Configurar Chatwoot
curl -X POST http://localhost:8080/chatwoot/config \
  -H "token: SEU_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "enabled": true,
    "account_id": "123",
    "token": "seu_chatwoot_token",
    "url": "https://app.chatwoot.com",
    "name_inbox": "WhatsApp Bot"
  }'

# 2. Testar conexão
curl -X POST http://localhost:8080/chatwoot/test \
  -H "token: SEU_TOKEN"

# 3. Verificar status
curl -X GET http://localhost:8080/chatwoot/status \
  -H "token: SEU_TOKEN"

# 4. Obter configuração
curl -X GET http://localhost:8080/chatwoot/config \
  -H "token: SEU_TOKEN"

# 5. Remover configuração
curl -X DELETE http://localhost:8080/chatwoot/config \
  -H "token: SEU_TOKEN"
```

### 🎯 Teste de Webhook (Chatwoot → WhatsApp):
```bash
# Simular webhook do Chatwoot
curl -X POST http://localhost:8080/chatwoot/webhook/SEU_USER_ID \
  -H "Content-Type: application/json" \
  -d '{
    "event": "message_created",
    "message_type": "outgoing",
    "private": false,
    "content": "Mensagem de teste",
    "conversation": {
      "id": 123,
      "meta": {
        "sender": {
          "identifier": "5511999999999@s.whatsapp.net",
          "phone_number": "+5511999999999"
        }
      }
    }
  }'
```

---

## 📝 Notas de Implementação

### ⚠️ Tratamento de Erros:
```go
// Padrão de retry para API Chatwoot
func retryWithBackoff(fn func() error, maxRetries int) error {
    for i := 0; i < maxRetries; i++ {
        if err := fn(); err == nil {
            return nil
        }
        time.Sleep(time.Duration(i+1) * time.Second)
    }
    return fmt.Errorf("max retries exceeded")
}
```

### 🚀 Performance:
- **Cache TTL:** 30 minutos para contatos/conversas
- **Rate Limiting:** Max 10 req/sec para API Chatwoot
- **Goroutines:** Processamento assíncrono de eventos
- **Connection Pool:** Reutilizar conexões HTTP

### 🔒 Segurança:
- **Token Validation:** Validar tokens antes de operações
- **Input Sanitization:** Escapar dados de entrada
- **Rate Limiting:** Prevenir abuse em endpoints
- **Secure Logs:** Não loggar tokens/dados sensíveis

---

## 🔧 Configuração de Ambiente

### 📦 Dependências Go (go.mod):
```go
// Verificar se já existem, caso contrário adicionar:
require (
    github.com/patrickmn/go-cache v2.1.0+incompatible // Para cache
    github.com/gorilla/mux v1.8.0                      // Para roteamento (já existe)
    github.com/jmoiron/sqlx v1.3.5                     // Para DB (já existe)
)
```

### 🌍 Variáveis de Ambiente (Opcionais):
```bash
# Configurações globais Chatwoot
export CHATWOOT_DEFAULT_TIMEOUT=30s
export CHATWOOT_CACHE_TTL=30m
export CHATWOOT_MAX_RETRIES=3
export CHATWOOT_RATE_LIMIT=10  # requests por segundo
```

### 🗄️ Database Schema:
```sql
-- Migration será executada automaticamente
-- Índices para performance:
CREATE INDEX idx_chatwoot_configs_user_id ON chatwoot_configs(user_id);
CREATE INDEX idx_chatwoot_configs_enabled ON chatwoot_configs(enabled);
```

---

## 📊 Métricas de Sucesso

### 🎯 KPIs Técnicos:
- **Latência:** < 500ms para processar mensagem WhatsApp → Chatwoot
- **Throughput:** > 100 mensagens/segundo por instância
- **Uptime:** > 99.9% de disponibilidade
- **Error Rate:** < 0.1% de falhas na sincronização

### 👥 KPIs de Usabilidade:
- **Setup Time:** < 5 minutos para configurar Chatwoot
- **Learning Curve:** Dashboard intuitivo sem necessidade de documentação
- **Support Overhead:** < 1% de tickets relacionados à integração

### 📈 KPIs de Negócio:
- **Adoption Rate:** > 80% dos usuários ativos usando Chatwoot
- **Response Time:** Redução de 50% no tempo de resposta
- **Customer Satisfaction:** Score > 4.5/5 na satisfação com atendimento

---

## 🎉 Entrega Final

### ✅ Checklist de Entrega:
- [ ] **Todos os arquivos criados** conforme especificação
- [ ] **Migrations executando** sem erros
- [ ] **Dashboard funcional** com todas as seções
- [ ] **API endpoints** respondendo corretamente
- [ ] **Webhook bidirecional** funcionando
- [ ] **Cache implementado** e otimizado
- [ ] **Logs estruturados** para debugging
- [ ] **Documentação atualizada** no README
- [ ] **Testes manuais** realizados e aprovados

### 📋 Entregáveis:
1. **Código fonte** completo com todos os arquivos
2. **Documentação** de configuração (README update)
3. **Scripts de teste** para validação
4. **Guia de troubleshooting** com problemas comuns

---

*📅 Documento criado em: 2024-12-21*  
*📝 Versão: 2.0 - FASES 1, 2, 3 e 5 CONCLUÍDAS + Interface Web Completa*  
*👨‍💻 Para: Integração Chatwoot + Wuzapi*  

---

## 🐳 **Docker Build para Desenvolvimento**

### **Criação de Imagem de Desenvolvimento:**
```bash
# Build da imagem de desenvolvimento
docker build -t wuzapi:dev .

# Executar container de desenvolvimento
docker run -d \
  --name wuzapi-dev \
  -p 8080:8080 \
  -e DB_TYPE=sqlite \
  -v $(pwd)/data:/app/data \
  wuzapi:dev

# Verificar logs do container
docker logs -f wuzapi-dev
```

### **Teste da Integração Chatwoot:**
```bash
# 1. Acessar dashboard
curl http://localhost:8080/dashboard

# 2. Configurar Chatwoot via API
curl -X POST http://localhost:8080/chatwoot/config \
  -H "token: SEU_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "enabled": true,
    "account_id": "123",
    "token": "seu_chatwoot_token",
    "url": "https://app.chatwoot.com",
    "name_inbox": "WhatsApp Bot"
  }'

# 3. Testar status
curl http://localhost:8080/chatwoot/status \
  -H "token: SEU_TOKEN"
```

---

## 📋 **Log de Atualizações**

### **2024-12-21 - v2.0** 🚀
- **FASE 3 CONCLUÍDA**: Interceptação e processamento de eventos WhatsApp
- **FASE 5 CONCLUÍDA**: Interface web completa com dashboard
- **FUNCIONALIDADES**: WhatsApp → Chatwoot funcionando para mensagens de texto
- **INTERFACE**: Modal completo de configuração com status em tempo real
- **DOCKER**: Imagem :dev criada para testes
- **STATUS**: Pronto para uso em desenvolvimento e testes

### **2024-12-21 - v1.1** ✅
- **FASE 1 CONCLUÍDA**: Estrutura base, database, handlers, rotas  
- **FASE 2 CONCLUÍDA**: Cliente HTTP, cache, integração global
- **REORGANIZAÇÃO**: Código movido para `chatwoot/` package
- **MELHORIAS**: Tipos limpos, wrappers, entry points organizados
- **STATUS**: Base sólida pronta para FASE 3 (Eventos WhatsApp)

### **2024-12-21 - v1.0** 📝
- Planejamento detalhado inicial
- Arquitetura e fases definidas  
- Especificações técnicas completas