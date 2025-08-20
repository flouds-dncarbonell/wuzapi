package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
)

func Find(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// Update entry in User map
func updateUserInfo(values interface{}, field string, value string) interface{} {
	log.Debug().Str("field", field).Str("value", value).Msg("User info updated")
	values.(Values).m[field] = value
	return values
}

// webhook for regular messages
func callHook(myurl string, payload map[string]string, id string) {
	log.Info().Str("url", myurl).Msg("Sending POST to client " + id)

	// Log the payload map
	log.Debug().Msg("Payload:")
	for key, value := range payload {
		log.Debug().Str(key, value).Msg("")
	}

	client := clientManager.GetHTTPClient(id)

	format := os.Getenv("WEBHOOK_FORMAT")
	if format == "json" {
		// Send as pure JSON
		// The original payload is a map[string]string, but we want to send the postmap (map[string]interface{})
		// So we try to decode the jsonData field if it exists, otherwise we send the original payload
		var body interface{} = payload
		if jsonStr, ok := payload["jsonData"]; ok {
			var postmap map[string]interface{}
			err := json.Unmarshal([]byte(jsonStr), &postmap)
			if err == nil {
				postmap["token"] = payload["token"]
				body = postmap
			}
		}
		_, err := client.R().
			SetHeader("Content-Type", "application/json").
			SetBody(body).
			Post(myurl)
		if err != nil {
			log.Debug().Str("error", err.Error())
		}
	} else {
		// Default: send as form-urlencoded
		_, err := client.R().SetFormData(payload).Post(myurl)
		if err != nil {
			log.Debug().Str("error", err.Error())
		}
	}
}

// webhook for messages with file attachments
func callHookFile(myurl string, payload map[string]string, id string, file string) error {
	log.Info().Str("file", file).Str("url", myurl).Msg("Sending POST")

	client := clientManager.GetHTTPClient(id)

	// Create final payload map
	finalPayload := make(map[string]string)
	for k, v := range payload {
		finalPayload[k] = v
	}

	finalPayload["file"] = file

	log.Debug().Interface("finalPayload", finalPayload).Msg("Final payload to be sent")

	resp, err := client.R().
		SetFiles(map[string]string{
			"file": file,
		}).
		SetFormData(finalPayload).
		Post(myurl)

	if err != nil {
		log.Error().Err(err).Str("url", myurl).Msg("Failed to send POST request")
		return fmt.Errorf("failed to send POST request: %w", err)
	}

	log.Debug().Interface("payload", finalPayload).Msg("Payload sent to webhook")
	log.Info().Int("status", resp.StatusCode()).Str("body", string(resp.Body())).Msg("POST request completed")

	return nil
}

type UserWebhook struct {
	ID     string `db:"id"`
	UserID string `db:"user_id"`
	URL    string `db:"url"`
	Events string `db:"events"`
}

func getUserWebhooks(db *sqlx.DB, userID string) ([]UserWebhook, error) {
	hooks := []UserWebhook{}
	err := db.Select(&hooks, "SELECT id, user_id, url, events FROM user_webhooks WHERE user_id=$1", userID)
	if err != nil {
		return nil, err
	}
	return hooks, nil
}

func getUserSubscribedEvents(db *sqlx.DB, userID string) ([]string, error) {
	hooks, err := getUserWebhooks(db, userID)
	if err != nil {
		return nil, err
	}
	eventsSet := make(map[string]struct{})
	for _, h := range hooks {
		if h.Events == "" {
			continue
		}
		for _, ev := range strings.Split(h.Events, ",") {
			ev = strings.TrimSpace(ev)
			if ev != "" && Find(supportedEventTypes, ev) {
				eventsSet[ev] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(eventsSet))
	for ev := range eventsSet {
		result = append(result, ev)
	}
	return result, nil
}

func dispatchUserWebhooks(db *sqlx.DB, userID, token, eventType string, jsonData []byte, path string) {
	hooks, err := getUserWebhooks(db, userID)
	if err != nil {
		log.Warn().Err(err).Str("userID", userID).Msg("Could not get user webhooks")
		return
	}

	instanceName := ""
	if userinfo, found := userinfocache.Get(token); found {
		instanceName = userinfo.(Values).Get("Name")
	}

	for _, h := range hooks {
		events := strings.Split(h.Events, ",")
		if len(events) > 0 {
			if !Find(events, eventType) && !Find(events, "All") {
				continue
			}
		}

		data := map[string]string{
			"jsonData":     string(jsonData),
			"token":        token,
			"instanceName": instanceName,
		}

		if path == "" {
			go callHook(h.URL, data, userID)
		} else {
			go func(url string) {
				if err := callHookFile(url, data, userID, path); err != nil {
					log.Error().Err(err).Str("url", url).Msg("Error calling hook file")
				}
			}(h.URL)
		}
	}
}

func (s *server) respondWithJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Error().Err(err).Msg("Failed to encode JSON response")
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// ProcessOutgoingMedia handles media processing for outgoing messages with S3 support
func ProcessOutgoingMedia(userID string, contactJID string, messageID string, data []byte, mimeType string, fileName string, db *sqlx.DB) (map[string]interface{}, error) {
	// Check if S3 is enabled for this user
	var s3Config struct {
		Enabled       bool   `db:"s3_enabled"`
		MediaDelivery string `db:"media_delivery"`
	}
	err := db.Get(&s3Config, "SELECT s3_enabled, media_delivery FROM users WHERE id = $1", userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get S3 config")
		s3Config.Enabled = false
		s3Config.MediaDelivery = "base64"
	}

	// Process S3 upload if enabled
	if s3Config.Enabled && (s3Config.MediaDelivery == "s3" || s3Config.MediaDelivery == "both") {
		// Process S3 upload (outgoing messages are always in outbox)
		s3Data, err := GetS3Manager().ProcessMediaForS3(
			context.Background(),
			userID,
			contactJID,
			messageID,
			data,
			mimeType,
			fileName,
			false, // isIncoming = false for sent messages
		)
		if err != nil {
			log.Error().Err(err).Msg("Failed to upload media to S3")
			// Continue even if S3 upload fails
		} else {
			return s3Data, nil
		}
	}

	return nil, nil
}
