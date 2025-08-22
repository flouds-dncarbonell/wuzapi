package chatwoot

import (
	"net/http"

	"github.com/jmoiron/sqlx"
)

// GlobalCache é a instância global do cache Chatwoot
var GlobalCache *Cache

// InitCache inicializa o cache global
func InitCache() {
	GlobalCache = NewCache()
}

// HandlerWrapper é um helper para criar handlers compatíveis com o servidor principal
type HandlerWrapper struct {
	db      *sqlx.DB
	respond func(http.ResponseWriter, *http.Request, int, interface{})
}

// NewHandlerWrapper cria um novo wrapper para os handlers
func NewHandlerWrapper(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) *HandlerWrapper {
	return &HandlerWrapper{
		db:      db,
		respond: respond,
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