package chatwoot

import (
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"
)

// Cache representa o sistema de cache para dados do Chatwoot
type Cache struct {
	contacts      *cache.Cache // phone -> Contact
	conversations *cache.Cache // contactID:inboxID -> Conversation
	inboxes       *cache.Cache // accountID -> []Inbox
}

// NewCache cria uma nova instância do cache Chatwoot
func NewCache() *Cache {
	return &Cache{
		contacts:      cache.New(30*time.Minute, 10*time.Minute),
		conversations: cache.New(30*time.Minute, 10*time.Minute),
		inboxes:       cache.New(60*time.Minute, 20*time.Minute),
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

// SetConversation armazena uma conversa no cache
func (cc *Cache) SetConversation(contactID, inboxID int, conversation *Conversation) {
	if conversation != nil {
		key := cc.getConversationKey(contactID, inboxID)
		cc.conversations.Set(key, conversation, cache.DefaultExpiration)
	}
}

// InvalidateConversation remove uma conversa do cache
func (cc *Cache) InvalidateConversation(contactID, inboxID int) {
	key := cc.getConversationKey(contactID, inboxID)
	cc.conversations.Delete(key)
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