package chatwoot

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
	"go.mau.fi/whatsmeow"
)

// GlobalCache é a instância global do cache Chatwoot
var GlobalCache *Cache

// GlobalClientGetter é uma interface para obter clientes WhatsApp
var GlobalClientGetter ClientGetter

// ClientGetter interface para acessar clientes WhatsApp
type ClientGetter interface {
	GetWhatsmeowClient(userID string) *whatsmeow.Client
}

// InitCache inicializa o cache global
func InitCache() {
	GlobalCache = NewCache()
}

// SetClientGetter define o getter global para clientes
func SetClientGetter(getter ClientGetter) {
	GlobalClientGetter = getter
}

// HandlerWrapper é um helper para criar handlers compatíveis com o servidor principal
type HandlerWrapper struct {
	db              *sqlx.DB
	respond         func(http.ResponseWriter, *http.Request, int, interface{})
	webhookProcessor *WebhookProcessor
}

// NewHandlerWrapper cria um novo wrapper para os handlers
func NewHandlerWrapper(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) *HandlerWrapper {
	return &HandlerWrapper{
		db:               db,
		respond:          respond,
		webhookProcessor: NewWebhookProcessor(db),
	}
}

// SetConfig retorna o handler para configurar Chatwoot
func (h *HandlerWrapper) SetConfig() http.HandlerFunc {
	return SetConfigHandler(h.db, h.respond)
}

// GetConfig retorna o handler para obter configuração Chatwoot
func (h *HandlerWrapper) GetConfig() http.HandlerFunc {
	return GetConfigHandler(h.db, h.respond)
}

// DeleteConfig retorna o handler para deletar configuração Chatwoot
func (h *HandlerWrapper) DeleteConfig() http.HandlerFunc {
	return DeleteConfigHandler(h.db, h.respond)
}

// GetStatus retorna o handler para obter status Chatwoot
func (h *HandlerWrapper) GetStatus() http.HandlerFunc {
	return GetStatusHandler(h.db, h.respond)
}

// TestConnection retorna o handler para testar conexão Chatwoot
func (h *HandlerWrapper) TestConnection() http.HandlerFunc {
	return TestConnectionHandler(h.db, h.respond)
}

// Webhook retorna o handler para processar webhooks do Chatwoot
func (h *HandlerWrapper) Webhook() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Converter de http.HandlerFunc para interface compatível
		c := &contextAdapter{
			w: w,
			r: r,
		}
		
		// Processar webhook
		h.webhookProcessor.ProcessWebhook(c)
	}
}

// contextAdapter adapta http.ResponseWriter e *http.Request para WebhookContext
type contextAdapter struct {
	w http.ResponseWriter
	r *http.Request
}

func (c *contextAdapter) Param(key string) string {
	// Usar gorilla/mux para extrair parâmetros da URL
	vars := mux.Vars(c.r)
	if value, exists := vars[key]; exists {
		return value
	}
	return ""
}

func (c *contextAdapter) ShouldBindJSON(obj interface{}) error {
	return json.NewDecoder(c.r.Body).Decode(obj)
}

func (c *contextAdapter) JSON(code int, obj interface{}) {
	c.w.Header().Set("Content-Type", "application/json")
	c.w.WriteHeader(code)
	json.NewEncoder(c.w).Encode(obj)
}