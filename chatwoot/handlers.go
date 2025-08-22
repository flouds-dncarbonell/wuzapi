package chatwoot

import (
	"encoding/json"
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

// Values interface para compatibilidade com userinfo
type Values interface {
	Get(string) string
}

// Server interface para compatibilidade com o servidor principal
type Server interface {
	Respond(w http.ResponseWriter, r *http.Request, statusCode int, data interface{})
	GetDB() *sqlx.DB
}

// SetConfigHandler cria ou atualiza configuração Chatwoot
func SetConfigHandler(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Info().Msg("=== STARTING SAVE CHATWOOT CONFIG ===")
		
		userinfo := r.Context().Value("userinfo").(Values)
		userID := userinfo.Get("Id")
		
		log.Info().Str("userID", userID).Msg("Saving Chatwoot config for user")

		var config Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Error decoding Chatwoot config")
			respond(w, r, http.StatusBadRequest, "Invalid JSON format")
			return
		}
		
		log.Info().
			Str("userID", userID).
			Str("url", config.URL).
			Str("accountID", config.AccountID).
			Bool("enabled", config.Enabled).
			Bool("hasToken", config.Token != "").
			Str("nameInbox", config.NameInbox).
			Msg("Received config data")

		// Definir userID a partir do contexto
		config.UserID = userID

		// Validar configuração
		log.Info().Str("userID", userID).Msg("Validating config")
		if err := ValidateConfig(config); err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Invalid Chatwoot config")
			respond(w, r, http.StatusBadRequest, err.Error())
			return
		}

		// Testar conectividade com Chatwoot se habilitado
		if config.Enabled {
			log.Info().Str("userID", userID).Msg("Testing connection before saving")
			client := NewClient(config)
			if err := client.TestConnection(); err != nil {
				log.Error().Err(err).Str("userID", userID).Msg("Failed to connect to Chatwoot during save")
				respond(w, r, http.StatusBadRequest, "Unable to connect to Chatwoot: "+err.Error())
				return
			}
			log.Info().Str("userID", userID).Msg("Connection test passed during save")
		} else {
			log.Info().Str("userID", userID).Msg("Chatwoot disabled, skipping connection test")
		}

		// Salvar configuração no banco
		log.Info().Str("userID", userID).Msg("Saving config to database")
		if err := SaveConfig(db, config); err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Error saving Chatwoot config")
			respond(w, r, http.StatusInternalServerError, "Failed to save configuration")
			return
		}

		log.Info().Str("userID", userID).Bool("enabled", config.Enabled).Msg("Chatwoot config saved successfully")
		
		successResponse := map[string]interface{}{
			"status":  "success",
			"message": "Chatwoot configuration saved successfully",
		}
		responseJson, err := json.Marshal(successResponse)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal success response")
			respond(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to send success response")
		respond(w, r, http.StatusOK, string(responseJson))
	})
}

// GetConfigHandler retorna a configuração Chatwoot atual do usuário
func GetConfigHandler(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Info().Msg("=== STARTING GET CHATWOOT CONFIG ===")
		
		userinfo := r.Context().Value("userinfo").(Values)
		userID := userinfo.Get("Id")
		
		log.Info().Str("userID", userID).Msg("Getting Chatwoot config for user")

		config, err := GetConfigByUserID(db, userID)
		if err != nil {
			if err.Error() == "sql: no rows in result set" {
				log.Info().Str("userID", userID).Msg("No Chatwoot config found - returning 404")
				notFoundResponse := map[string]interface{}{
					"status":  "not_found",
					"message": "Chatwoot not configured",
				}
				responseJson, err := json.Marshal(notFoundResponse)
				if err != nil {
					log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal not found response")
					respond(w, r, http.StatusInternalServerError, err.Error())
					return
				}
				log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to send not found response")
				respond(w, r, http.StatusNotFound, string(responseJson))
				return
			}
			log.Error().Err(err).Str("userID", userID).Msg("Error getting Chatwoot config")
			log.Error().Str("errorMessage", err.Error()).Msg("Full error details")
			respond(w, r, http.StatusInternalServerError, "Failed to retrieve configuration")
			return
		}

		log.Info().Str("userID", userID).Msg("Config found successfully, preparing response")
		
		// Verificar se config não é nil
		if config == nil {
			log.Error().Str("userID", userID).Msg("Config is nil but no error returned")
			respond(w, r, http.StatusInternalServerError, "Configuration is nil")
			return
		}
		
		// Remover token por segurança
		configResponse := *config
		configResponse.Token = ""
		
		log.Info().
			Str("userID", userID).
			Str("url", configResponse.URL).
			Str("accountID", configResponse.AccountID).
			Bool("enabled", configResponse.Enabled).
			Str("nameInbox", configResponse.NameInbox).
			Bool("signMsg", configResponse.SignMsg).
			Bool("reopenConversation", configResponse.ReopenConversation).
			Bool("conversationPending", configResponse.ConversationPending).
			Str("ignoreJids", configResponse.IgnoreJids).
			Msg("Found Chatwoot config - returning data")

		log.Info().Str("userID", userID).Msg("About to marshal config response")
		responseJson, err := json.Marshal(configResponse)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal config response")
			respond(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		
		log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to call respond() function")
		respond(w, r, http.StatusOK, string(responseJson))
		log.Info().Str("userID", userID).Msg("Response sent successfully")
	})
}

// DeleteConfigHandler remove a configuração Chatwoot do usuário
func DeleteConfigHandler(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userinfo := r.Context().Value("userinfo").(Values)
		userID := userinfo.Get("Id")

		if err := DeleteConfig(db, userID); err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Error deleting Chatwoot config")
			respond(w, r, http.StatusInternalServerError, "Failed to delete configuration")
			return
		}

		log.Info().Str("userID", userID).Msg("Chatwoot config deleted")
		
		deleteResponse := map[string]interface{}{
			"status":  "success",
			"message": "Chatwoot configuration deleted successfully",
		}
		responseJson, err := json.Marshal(deleteResponse)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal delete response")
			respond(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to send delete response")
		respond(w, r, http.StatusOK, string(responseJson))
	})
}

// GetStatusHandler retorna o status da integração Chatwoot
func GetStatusHandler(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userinfo := r.Context().Value("userinfo").(Values)
		userID := userinfo.Get("Id")

		// Buscar configuração
		config, err := GetConfigByUserID(db, userID)
		if err != nil {
			if err.Error() == "sql: no rows in result set" {
				noConfigStatus := map[string]interface{}{
					"configured": false,
					"connected":  false,
					"stats": map[string]interface{}{
						"messages_sent":        0,
						"active_conversations": 0,
						"last_sync":           "Nunca",
					},
				}
				responseJson, err := json.Marshal(noConfigStatus)
				if err != nil {
					log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal no config status response")
					respond(w, r, http.StatusInternalServerError, err.Error())
					return
				}
				log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to send no config status response")
				respond(w, r, http.StatusOK, string(responseJson))
				return
			}
			log.Error().Err(err).Str("userID", userID).Msg("Error getting Chatwoot config for status")
			respond(w, r, http.StatusInternalServerError, "Failed to retrieve status")
			return
		}

		status := map[string]interface{}{
			"configured": true,
			"enabled":    config.Enabled,
			"connected":  false,
			"stats": map[string]interface{}{
				"messages_sent":        0,
				"active_conversations": 0,
				"last_sync":           "Nunca",
			},
		}

		// Testar conectividade se habilitado
		if config.Enabled {
			client := NewClient(*config)
			if err := client.TestConnection(); err == nil {
				status["connected"] = true
			} else {
				log.Debug().Err(err).Str("userID", userID).Msg("Chatwoot connection test failed")
			}

			// Buscar estatísticas (implementar conforme necessidade)
			stats := getChatwootStats(db, userID)
			status["stats"] = stats
		}

		responseJson, err := json.Marshal(status)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal status response")
			respond(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to send status response")
		respond(w, r, http.StatusOK, string(responseJson))
	})
}

// TestConnectionHandler testa a conectividade com Chatwoot
func TestConnectionHandler(db *sqlx.DB, respond func(http.ResponseWriter, *http.Request, int, interface{})) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Info().Msg("=== STARTING CHATWOOT CONNECTION TEST ===")
		
		// Log da requisição recebida
		log.Info().
			Str("method", r.Method).
			Str("url", r.URL.String()).
			Str("contentType", r.Header.Get("Content-Type")).
			Int64("contentLength", r.ContentLength).
			Msg("Received test connection request")
		
		// Log dos headers importantes
		log.Info().
			Str("token", r.Header.Get("token")).
			Str("userAgent", r.Header.Get("User-Agent")).
			Msg("Request headers")
		
		// Verificar se o context tem userinfo
		userinfo := r.Context().Value("userinfo")
		if userinfo == nil {
			log.Error().Msg("userinfo context is nil - authentication middleware issue?")
			respond(w, r, http.StatusBadRequest, "Authentication context missing")
			return
		}
		
		userinfoValues, ok := userinfo.(Values)
		if !ok {
			log.Error().Msg("userinfo context is not Values type")
			respond(w, r, http.StatusBadRequest, "Invalid authentication context")
			return
		}
		
		userID := userinfoValues.Get("Id")
		if userID == "" {
			log.Error().Msg("userID is empty in context")
			respond(w, r, http.StatusBadRequest, "User ID missing")
			return
		}
		
		log.Info().Str("userID", userID).Msg("Testing Chatwoot connection for user")

		var testConfig Config
		var useFormData bool = false
		
		// Primeiro, tentar obter configuração do banco
		savedConfig, err := GetConfigByUserID(db, userID)
		
		if err != nil || savedConfig == nil || !savedConfig.Enabled {
			// Não há config salva OU está desabilitada - tentar usar dados do formulário
			log.Info().Str("userID", userID).Msg("No saved config or disabled - attempting to use form data")
			
			// Parse dados do corpo da requisição
			var formConfig Config
			if err := json.NewDecoder(r.Body).Decode(&formConfig); err != nil {
				log.Error().Err(err).Str("userID", userID).Msg("No saved config and invalid form data")
				respond(w, r, http.StatusBadRequest, "No saved configuration and invalid form data provided")
				return
			}
			
			// Definir user_id para o teste (necessário para validação)
			formConfig.UserID = userID
			
			log.Info().
				Str("userID", userID).
				Str("url", formConfig.URL).
				Str("accountID", formConfig.AccountID).
				Bool("hasToken", formConfig.Token != "").
				Msg("Using form data for connection test")
			
			// Validar dados do formulário
			if err := ValidateConfig(formConfig); err != nil {
				log.Error().Err(err).Str("userID", userID).Msg("Invalid form data for test")
				respond(w, r, http.StatusBadRequest, "Invalid configuration data: "+err.Error())
				return
			}
			
			testConfig = formConfig
			useFormData = true
		} else {
			// Usar configuração salva do banco
			log.Info().
				Str("userID", userID).
				Str("url", savedConfig.URL).
				Str("accountID", savedConfig.AccountID).
				Bool("enabled", savedConfig.Enabled).
				Bool("hasToken", savedConfig.Token != "").
				Msg("Using saved config for connection test")
			
			testConfig = *savedConfig
		}

		log.Info().
			Str("userID", userID).
			Bool("useFormData", useFormData).
			Msg("Creating Chatwoot client for test")
		
		client := NewClient(testConfig)
		
		log.Info().Str("userID", userID).Msg("Starting connection test")
		if err := client.TestConnection(); err != nil {
			log.Error().
				Err(err).
				Str("userID", userID).
				Str("url", testConfig.URL).
				Str("accountID", testConfig.AccountID).
				Bool("useFormData", useFormData).
				Msg("Chatwoot connection test failed")
			respond(w, r, http.StatusBadRequest, "Connection failed: "+err.Error())
			return
		}

		log.Info().
			Str("userID", userID).
			Bool("useFormData", useFormData).
			Msg("Chatwoot connection test successful")
		
		testResponse := map[string]interface{}{
			"status":  "success",
			"message": "Connection to Chatwoot successful",
		}
		responseJson, err := json.Marshal(testResponse)
		if err != nil {
			log.Error().Err(err).Str("userID", userID).Msg("Failed to marshal test response")
			respond(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		log.Info().Str("userID", userID).Str("responseJson", string(responseJson)).Msg("About to send test response")
		respond(w, r, http.StatusOK, string(responseJson))
	})
}

// getChatwootStats busca estatísticas do banco de dados
func getChatwootStats(db *sqlx.DB, userID string) map[string]interface{} {
	// TODO: Implementar queries para estatísticas reais
	// Por exemplo: contar mensagens enviadas hoje, conversas ativas, etc.
	return map[string]interface{}{
		"messages_sent":        0,
		"active_conversations": 0,
		"last_sync":           "Nunca",
	}
}

// Função respondJSON removida - usando padrão S3 com json.Marshal direto nos handlers