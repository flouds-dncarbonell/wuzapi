package chatwoot

import (
	"sync"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

// userFlags armazena em memória se usuários têm Chatwoot habilitado
// Evita consultas repetidas ao banco de dados
var userFlags = sync.Map{} // userID -> bool

// SetUserChatwootEnabled define se um usuário tem Chatwoot habilitado
func SetUserChatwootEnabled(userID string, enabled bool) {
	userFlags.Store(userID, enabled)
	log.Debug().
		Str("userID", userID).
		Bool("enabled", enabled).
		Msg("Updated Chatwoot flag for user")
}

// IsUserChatwootEnabled verifica se um usuário tem Chatwoot habilitado
// Usa lazy loading: consulta banco apenas na primeira vez, depois usa cache
func IsUserChatwootEnabled(db *sqlx.DB, userID string) bool {
	// Verificar se já está em cache
	if val, exists := userFlags.Load(userID); exists {
		return val.(bool)
	}

	// Não está em cache - consultar banco (apenas uma vez por usuário)
	log.Debug().Str("userID", userID).Msg("Loading Chatwoot status from database")
	
	config, err := GetConfigByUserID(db, userID)
	enabled := err == nil && config != nil && config.Enabled

	// Armazenar no cache para próximas consultas
	userFlags.Store(userID, enabled)

	log.Debug().
		Str("userID", userID).
		Bool("enabled", enabled).
		Bool("fromCache", false).
		Msg("Loaded Chatwoot status")

	return enabled
}

// ClearUserChatwootFlag remove a flag de um usuário (útil para testes/limpeza)
func ClearUserChatwootFlag(userID string) {
	userFlags.Delete(userID)
	log.Debug().Str("userID", userID).Msg("Cleared Chatwoot flag for user")
}

// GetChatwootFlagsStats retorna estatísticas das flags em memória
func GetChatwootFlagsStats() map[string]interface{} {
	count := 0
	enabled := 0
	
	userFlags.Range(func(key, value interface{}) bool {
		count++
		if value.(bool) {
			enabled++
		}
		return true
	})

	return map[string]interface{}{
		"total_users": count,
		"enabled_users": enabled,
		"disabled_users": count - enabled,
	}
}