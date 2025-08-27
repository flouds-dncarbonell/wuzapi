package chatwoot

import (
	"fmt"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/rs/zerolog/log"
)

// Cache representa o sistema de cache para dados do Chatwoot
type Cache struct {
	contacts      *cache.Cache // phone -> Contact
	conversations *cache.Cache // contactID:inboxID -> Conversation
	inboxes       *cache.Cache // accountID -> []Inbox
	avatarChecks  *cache.Cache // phone -> timestamp da última verificação de avatar
	lastSeenCache *cache.Cache // conversationID -> timestamp do último update_last_seen
}

// NewCache cria uma nova instância do cache Chatwoot
func NewCache() *Cache {
	return &Cache{
		contacts:      cache.New(30*time.Minute, 10*time.Minute),
		conversations: cache.New(30*time.Minute, 10*time.Minute),
		inboxes:       cache.New(60*time.Minute, 20*time.Minute),
		avatarChecks:  cache.New(24*time.Hour, 6*time.Hour), // Cache de 24h para verificações de avatar
		lastSeenCache: cache.New(10*time.Minute, 2*time.Minute), // Cache de 10 min para throttle de last_seen
	}
}

// === MÉTODOS PARA CONTATOS ===

// GetContact busca um contato no cache pelo número de telefone
func (cc *Cache) GetContact(phone string) (*Contact, bool) {
	if contact, found := cc.contacts.Get(phone); found {
		if c, ok := contact.(*Contact); ok {
			return c, true
		}
	}
	return nil, false
}

// SetContact armazena um contato no cache
func (cc *Cache) SetContact(phone string, contact *Contact) {
	if contact != nil {
		cc.contacts.Set(phone, contact, cache.DefaultExpiration)
	}
}

// InvalidateContact remove um contato do cache
func (cc *Cache) InvalidateContact(phone string) {
	cc.contacts.Delete(phone)
}

// === MÉTODOS PARA VERIFICAÇÃO DE AVATAR ===

// ShouldCheckAvatar verifica se deve verificar o avatar do contato
// Retorna true se:
// - Nunca foi verificado
// - Última verificação foi há mais de 6 horas
func (cc *Cache) ShouldCheckAvatar(phone string) bool {
	if lastCheck, found := cc.avatarChecks.Get(phone); found {
		if timestamp, ok := lastCheck.(time.Time); ok {
			// Verificar novamente a cada 6 horas
			return time.Since(timestamp) > 6*time.Hour
		}
	}
	return true // Nunca foi verificado
}

// MarkAvatarChecked marca que o avatar foi verificado agora
func (cc *Cache) MarkAvatarChecked(phone string) {
	cc.avatarChecks.Set(phone, time.Now(), cache.DefaultExpiration)
}

// InvalidateAvatarCheck remove o timestamp de verificação de avatar
func (cc *Cache) InvalidateAvatarCheck(phone string) {
	cc.avatarChecks.Delete(phone)
}

// === MÉTODOS PARA THROTTLE DE LAST SEEN ===

// ShouldUpdateLastSeen verifica se deve fazer update_last_seen para uma conversa
// Retorna true se nunca foi atualizado ou se passou mais de 30 segundos desde última atualização
func (cc *Cache) ShouldUpdateLastSeen(conversationID int) bool {
	key := fmt.Sprintf("last_seen:%d", conversationID)
	if lastUpdate, found := cc.lastSeenCache.Get(key); found {
		if timestamp, ok := lastUpdate.(time.Time); ok {
			// Permitir update apenas após 30 segundos para evitar spam
			return time.Since(timestamp) > 30*time.Second
		}
	}
	return true // Nunca foi atualizado
}

// MarkLastSeenUpdated marca que o last_seen foi atualizado agora para uma conversa
func (cc *Cache) MarkLastSeenUpdated(conversationID int) {
	key := fmt.Sprintf("last_seen:%d", conversationID)
	cc.lastSeenCache.Set(key, time.Now(), cache.DefaultExpiration)
}

// HasProcessedReadReceipt verifica se um read receipt específico já foi processado
func (cc *Cache) HasProcessedReadReceipt(cacheKey string) bool {
	_, found := cc.lastSeenCache.Get(cacheKey)
	return found
}

// MarkReadReceiptProcessed marca um read receipt específico como processado
func (cc *Cache) MarkReadReceiptProcessed(cacheKey string) {
	// Usar TTL de 5 minutos para evitar reprocessamento de messageIDs duplicados
	cc.lastSeenCache.Set(cacheKey, time.Now(), 5*time.Minute)
}

// === MÉTODOS PARA CONVERSAS ===

// getConversationKey gera uma chave única para a conversa
func (cc *Cache) getConversationKey(contactID, inboxID int) string {
	return fmt.Sprintf("%d:%d", contactID, inboxID)
}

// GetConversation busca uma conversa no cache
func (cc *Cache) GetConversation(contactID, inboxID int) (*Conversation, bool) {
	key := cc.getConversationKey(contactID, inboxID)
	if conversation, found := cc.conversations.Get(key); found {
		if c, ok := conversation.(*Conversation); ok {
			return c, true
		}
	}
	return nil, false
}

// GetConversationByKey busca conversa no cache por chave customizada (como chatwoot-lib)
func (cc *Cache) GetConversationByKey(cacheKey string) (*Conversation, bool) {
	if conversation, found := cc.conversations.Get(cacheKey); found {
		if c, ok := conversation.(*Conversation); ok {
			return c, true
		}
	}
	return nil, false
}

// SetConversation armazena uma conversa no cache
func (cc *Cache) SetConversation(contactID, inboxID int, conversation *Conversation) {
	if conversation != nil {
		key := cc.getConversationKey(contactID, inboxID)
		cc.conversations.Set(key, conversation, cache.DefaultExpiration)
	}
}

// SetConversationByKey armazena conversa no cache por chave customizada (como chatwoot-lib)
func (cc *Cache) SetConversationByKey(cacheKey string, conversation *Conversation) {
	if conversation != nil {
		cc.conversations.Set(cacheKey, conversation, cache.DefaultExpiration)
	}
}

// GetCachedData busca dados genéricos no cache
func (cc *Cache) GetCachedData(key string) (interface{}, bool) {
	return cc.conversations.Get(key)
}

// SetCachedData armazena dados genéricos no cache com TTL personalizado
func (cc *Cache) SetCachedData(key string, value interface{}, ttlSeconds int) {
	ttl := time.Duration(ttlSeconds) * time.Second
	cc.conversations.Set(key, value, ttl)
}

// InvalidateConversation remove uma conversa do cache
func (cc *Cache) InvalidateConversation(contactID, inboxID int) {
	key := cc.getConversationKey(contactID, inboxID)
	cc.conversations.Delete(key)
}

// InvalidateConversationsByPrefix remove todas as conversas que começam com um prefixo
func (cc *Cache) InvalidateConversationsByPrefix(prefix string) {
	// Obter todos os itens do cache de conversas
	items := cc.conversations.Items()
	
	deletedCount := 0
	for key := range items {
		if strings.HasPrefix(key, prefix) {
			cc.conversations.Delete(key)
			deletedCount++
		}
	}
	
	log.Debug().
		Str("prefix", prefix).
		Int("deletedCount", deletedCount).
		Msg("Invalidated conversations by prefix")
}

// === MÉTODOS PARA INBOXES ===

// GetInboxes busca a lista de inboxes no cache
func (cc *Cache) GetInboxes(accountID string) ([]Inbox, bool) {
	if inboxes, found := cc.inboxes.Get(accountID); found {
		if i, ok := inboxes.([]Inbox); ok {
			return i, true
		}
	}
	return nil, false
}

// SetInboxes armazena a lista de inboxes no cache
func (cc *Cache) SetInboxes(accountID string, inboxes []Inbox) {
	if inboxes != nil {
		cc.inboxes.Set(accountID, inboxes, cache.DefaultExpiration)
	}
}

// InvalidateInboxes remove a lista de inboxes do cache
func (cc *Cache) InvalidateInboxes(accountID string) {
	cc.inboxes.Delete(accountID)
}

// === MÉTODOS PARA LIMPEZA ===

// ClearUserCache limpa todo o cache relacionado a um usuário
func (cc *Cache) ClearUserCache(userID string) {
	// Como não temos uma relação direta userID -> cache keys,
	// vamos limpar todo o cache por segurança
	cc.contacts.Flush()
	cc.conversations.Flush()
	cc.inboxes.Flush()
}

// GetCacheStats retorna estatísticas do cache
func (cc *Cache) GetCacheStats() map[string]interface{} {
	return map[string]interface{}{
		"contacts": map[string]interface{}{
			"items": cc.contacts.ItemCount(),
		},
		"conversations": map[string]interface{}{
			"items": cc.conversations.ItemCount(),
		},
		"inboxes": map[string]interface{}{
			"items": cc.inboxes.ItemCount(),
		},
		"lastSeenCache": map[string]interface{}{
			"items": cc.lastSeenCache.ItemCount(),
		},
	}
}

// === FUNÇÕES AUXILIARES PARA INTEGRAÇÃO ===

// FindOrCreateContact busca no cache ou na API e cria se necessário
func FindOrCreateContact(client *Client, phone, name string, inboxID int) (*Contact, error) {
	// 1. Verificar cache
	if contact, found := GlobalCache.GetContact(phone); found {
		return contact, nil
	}

	// 2. Buscar no Chatwoot
	contact, err := client.FindContact(phone)
	if err == nil && contact != nil {
		GlobalCache.SetContact(phone, contact)
		return contact, nil
	}

	// 3. Criar novo contato
	if name == "" {
		name = phone
	}
	contact, err = client.CreateContact(phone, name, "", inboxID)
	if err != nil {
		return nil, err
	}

	GlobalCache.SetContact(phone, contact)
	return contact, nil
}

// FindOrCreateConversation busca no cache ou na API e cria se necessário
func FindOrCreateConversation(client *Client, contactID, inboxID int) (*Conversation, error) {
	// 1. Verificar cache
	if conversation, found := GlobalCache.GetConversation(contactID, inboxID); found {
		return conversation, nil
	}

	// 2. Buscar no Chatwoot
	conversation, err := client.FindConversation(contactID, inboxID)
	if err == nil && conversation != nil {
		GlobalCache.SetConversation(contactID, inboxID, conversation)
		return conversation, nil
	}

	// 3. Criar nova conversa
	conversation, err = client.CreateConversation(contactID, inboxID)
	if err != nil {
		return nil, err
	}

	GlobalCache.SetConversation(contactID, inboxID, conversation)
	return conversation, nil
}

// GetCachedInboxes busca inboxes no cache ou na API
func GetCachedInboxes(client *Client, accountID string) ([]Inbox, error) {
	// 1. Verificar cache
	if inboxes, found := GlobalCache.GetInboxes(accountID); found {
		return inboxes, nil
	}

	// 2. Buscar no Chatwoot
	inboxes, err := client.ListInboxes()
	if err != nil {
		return nil, err
	}

	GlobalCache.SetInboxes(accountID, inboxes)
	return inboxes, nil
}