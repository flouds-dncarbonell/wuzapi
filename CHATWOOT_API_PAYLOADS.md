# Payloads da Integração Chatwoot - Wuzapi

Este documento apresenta todos os payloads que enviamos para a API do Chatwoot durante a integração. É essencial verificar se estamos usando os campos de forma correta conforme a documentação oficial da API.

## 📋 Índice
1. [Busca de Contatos](#busca-de-contatos)
2. [Criação de Contatos](#criação-de-contatos)
3. [Criação de Conversas](#criação-de-conversas)
4. [Envio de Mensagens](#envio-de-mensagens)
5. [Anexos/Attachments](#anexosattachments)
6. [Status de Conversas](#status-de-conversas)
7. [Headers HTTP](#headers-http)
8. [Endpoints Utilizados](#endpoints-utilizados)

---

## 🔍 Busca de Contatos

### Endpoint: `POST /api/v1/accounts/{account_id}/contacts/filter`

**Payload para busca otimizada com OR:**
```json
{
  "payload": [
    {
      "attribute_key": "phone_number",
      "filter_operator": "equal_to",
      "values": ["5511999999999"],
      "query_operator": "OR"
    },
    {
      "attribute_key": "identifier",
      "filter_operator": "equal_to", 
      "values": ["5511999999999@s.whatsapp.net"],
      "query_operator": null
    }
  ]
}
```

### 📋 Diferenças entre Conversas Individuais e Grupos

**Para conversas individuais:**
```json
{
  "payload": [
    {
      "attribute_key": "phone_number",
      "filter_operator": "equal_to",
      "values": ["5511999999999"],
      "query_operator": "OR"
    },
    {
      "attribute_key": "identifier",
      "filter_operator": "equal_to",
      "values": ["5511999999999@s.whatsapp.net"],
      "query_operator": null
    }
  ]
}
```

**Para grupos (tratados como contatos únicos):**
```json
{
  "payload": [
    {
      "attribute_key": "phone_number",
      "filter_operator": "equal_to",
      "values": ["120363123456789012"],
      "query_operator": "OR"
    },
    {
      "attribute_key": "identifier",
      "filter_operator": "equal_to",
      "values": ["120363123456789012@g.us"],
      "query_operator": null
    }
  ]
}
```

**Características importantes:**
- ✅ **Phone number SEM +**: Seguimos padrão da chatwoot-lib (sem prefixo +)
- ✅ **Query com OR**: Busca por `phone_number` OU `identifier`
- ✅ **Último query_operator**: Sempre `null` no último filtro
- ✅ **Individual format**: `{phone}@s.whatsapp.net` (WhatsApp individual)
- ✅ **Group format**: `{group_id}@g.us` (WhatsApp group)
- 🔄 **Identificador único**: Individual usa telefone, grupo usa ID do grupo

---

## 👤 Criação de Contatos

### Endpoint: `POST /api/v1/accounts/{account_id}/contacts`

**Payload para contato individual:**
```json
{
  "inbox_id": 123,
  "name": "João Silva",
  "phone_number": "+5511999999999",
  "identifier": "5511999999999@s.whatsapp.net"
}
```

**Payload para grupo (como contato único):**
```json
{
  "inbox_id": 123,
  "name": "Grupo da Família (GROUP)",
  "phone_number": "120363123456789012",
  "identifier": "120363123456789012@g.us"
}
```

**Com avatar (opcional - ambos os tipos):**
```json
{
  "inbox_id": 123,
  "name": "João Silva", 
  "phone_number": "+5511999999999",
  "identifier": "5511999999999@s.whatsapp.net",
  "avatar_url": "https://example.com/avatar.jpg"
}
```

### 🆔 Tratamento de Identificadores

**Para conversas individuais:**
- **Phone number**: `+{numero}` (formato internacional com +)
- **Identifier**: `{numero}@s.whatsapp.net` (sem +)
- **Fonte**: `evt.Info.Chat.User` (sempre o telefone do contato)

**Para grupos:**
- **Phone number**: `{group_id}` (ID do grupo, SEM +)
- **Identifier**: `{group_id}@g.us` (formato grupo WhatsApp)
- **Nome**: `{nome_grupo} (GROUP)` para diferenciação visual
- **Fonte**: `evt.Info.Chat.User` (ID único do grupo)

**Características importantes:**
- ✅ **Individual COM +**: Telefones individuais usam formato internacional (+)
- ✅ **Grupo SEM +**: IDs de grupo não recebem prefixo +
- ✅ **Identifier sempre SEM +**: Mantém formato WhatsApp original
- ✅ **InboxID obrigatório**: Necessário para associar ao inbox correto
- ✅ **Name fallback**: Se name vazio, usa phone/group_id como fallback
- 🔄 **Diferenciação visual**: Grupos recebem sufixo "(GROUP)" no nome

---

## 💬 Criação de Conversas

### Endpoint: `POST /api/v1/accounts/{account_id}/conversations`

**Payload padrão (seguindo chatwoot-lib):**
```json
{
  "contact_id": "456",
  "inbox_id": "123"
}
```

**Payload com source_id (método alternativo):**
```json
{
  "contact_id": 456,
  "inbox_id": 123,
  "source_id": "whatsapp-message-id-12345",
  "status": "open"
}
```

**Características importantes:**
- ⚠️ **IDs como strings**: Seguindo chatwoot-lib, IDs vão como strings no payload padrão
- ⚠️ **IDs como numbers**: No método com source_id, IDs vão como números
- ✅ **Status padrão**: Chatwoot cria automaticamente como "open"
- ✅ **Source ID**: Opcional, usado para rastreamento de mensagens WhatsApp

---

## 📨 Envio de Mensagens

### Endpoint: `POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages`

**Payload para mensagem individual (texto simples):**
```json
{
  "content": "Olá! Como posso ajudá-lo hoje?",
  "message_type": "incoming"
}
```

**Payload para mensagem de grupo (com identificação do remetente):**
```json
{
  "content": "[+55 (11) 99999-9999 - João Silva]: Olá pessoal! Como estão?",
  "message_type": "incoming",
  "source_id": "whatsapp-msg-id-67890"
}
```

**Payload para mensagem do bot em grupo:**
```json
{
  "content": "[Bot]: Mensagem enviada pelo bot",
  "message_type": "outgoing",
  "source_id": "whatsapp-msg-id-12345"
}
```

**Payload com source_id (para rastreamento):**
```json
{
  "content": "Mensagem recebida do WhatsApp",
  "message_type": "incoming",
  "source_id": "whatsapp-msg-id-67890"
}
```

**Payload para mensagem enviada pelo usuário (fromMe=true):**
```json
{
  "content": "Mensagem enviada pelo celular do usuário",
  "message_type": "outgoing",
  "source_id": "whatsapp-msg-id-12345"
}
```

**Payload para mensagem recebida (fromMe=false):**
```json
{
  "content": "Resposta recebida do contato",
  "message_type": "incoming",
  "source_id": "whatsapp-msg-id-67890"
}
```

### 📝 Formatação de Mensagens por Tipo

**Para conversas individuais:**
- **Conteúdo direto**: Texto da mensagem sem prefixos
- **Identificação**: Baseada apenas no `message_type` (incoming/outgoing)

**Para grupos:**
- **Formato com remetente**: `[{telefone_formatado} - {nome}]: {conteúdo}`
- **Bot messages**: `[Bot]: {conteúdo}` para mensagens fromMe=true
- **Telefone formatado**: `+55 (11) 99999-9999 - Nome do Contato`
- **Fallback nome**: "Desconhecido" se PushName estiver vazio

**Características importantes:**
- ✅ **Message types**: `"incoming"` = recebida, `"outgoing"` = enviada
- ✅ **Source ID opcional**: Para rastrear mensagens específicas do WhatsApp
- ✅ **Content obrigatório**: Texto da mensagem sempre necessário
- ✅ **FromMe Logic**: Mensagens `fromMe=true` são enviadas como `message_type: "outgoing"`
- ✅ **Histórico Completo**: Operador vê ambos os lados da conversa no Chatwoot
- 🔄 **Formatação Contextual**: Grupos incluem identificação do remetente no conteúdo
- 🔄 **Telefone Formatado**: Números brasileiros em formato `+55 (XX) XXXXX-XXXX`

---

## 📎 Anexos/Attachments

### Endpoint: `POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages`

**Multipart Form Data:**
```
Content-Type: multipart/form-data; boundary=----WebKitFormBoundary...

------WebKitFormBoundary...
Content-Disposition: form-data; name="content"

Legenda da imagem (opcional)
------WebKitFormBoundary...
Content-Disposition: form-data; name="message_type"

0
------WebKitFormBoundary...
Content-Disposition: form-data; name="attachments[]"; filename="image.jpg"
Content-Type: image/jpeg

[binary file data]
------WebKitFormBoundary...--
```

**Características importantes:**
- ✅ **Multipart required**: Anexos sempre via multipart form data
- ✅ **Field name**: `attachments[]` (com colchetes para array)
- ✅ **Content opcional**: Caption/legenda pode ser vazia
- ✅ **Message type**: 0 para mensagens recebidas, 1 para enviadas

---

## 🔄 Status de Conversas

### Endpoint: `POST /api/v1/accounts/{account_id}/conversations/{conversation_id}/toggle_status`

**Payload:**
```json
{
  "status": "open"
}
```

**Status válidos:**
- `"open"` - Conversa ativa
- `"resolved"` - Conversa resolvida/fechada
- `"pending"` - Aguardando resposta

**Características importantes:**
- ✅ **Status strings**: Sempre como string, não enum
- ✅ **Toggle endpoint**: Usa endpoint específico para mudança de status

---

## 🔑 Headers HTTP

**Headers padrão para todas as requisições:**
```http
Content-Type: application/json
User-Agent: wuzapi-chatwoot/1.0
api_access_token: {seu_token_chatwoot}
```

**Para anexos (multipart):**
```http
Content-Type: multipart/form-data; boundary=...
User-Agent: wuzapi-chatwoot/1.0
api_access_token: {seu_token_chatwoot}
```

**Características importantes:**
- ✅ **Authentication**: Via header `api_access_token` (não Bearer)
- ✅ **User Agent**: Identificação customizada
- ✅ **Content-Type**: JSON para API calls, multipart para anexos

---

## 🔗 Endpoints Utilizados

| Funcionalidade | Método | Endpoint | Payload |
|---|---|---|---|
| **Teste Conexão** | GET | `/api/v1/accounts/{account_id}/inboxes` | ❌ Nenhum |
| **Buscar Contato** | POST | `/api/v1/accounts/{account_id}/contacts/filter` | ✅ Filter payload |
| **Criar Contato** | POST | `/api/v1/accounts/{account_id}/contacts` | ✅ Contact data |
| **Listar Conversas** | GET | `/api/v1/accounts/{account_id}/contacts/{contact_id}/conversations` | ❌ Nenhum |
| **Criar Conversa** | POST | `/api/v1/accounts/{account_id}/conversations` | ✅ IDs + source_id |
| **Enviar Mensagem** | POST | `/api/v1/accounts/{account_id}/conversations/{conversation_id}/messages` | ✅ Content + type |
| **Enviar Anexo** | POST | `/api/v1/accounts/{conversation_id}/messages` | ✅ Multipart form |
| **Mudar Status** | POST | `/api/v1/accounts/{account_id}/conversations/{conversation_id}/toggle_status` | ✅ Status string |
| **Listar Inboxes** | GET | `/api/v1/accounts/{account_id}/inboxes` | ❌ Nenhum |

---

## 📥 Resposta da API Chatwoot

### Listar Inboxes - Response Payload

**Endpoint:** `GET /api/v1/accounts/{account_id}/inboxes`

**Resposta da API:**
```json
{
  "payload": [
    {
      "id": 73,
      "name": "Business",
      "channel_type": "Channel::Whatsapp",
      "phone_number": "+5551998641731",
      "provider": "evolution",
      "callback_webhook_url": "https://chatme.flouds.com.br/webhooks/whatsapp/+5551998641731",
      "provider_config": {
        "api_url": "https://evo.flouds.com.br",
        "admin_token": "7fa2b8dadab205a6dcbbe79d4a03cf2c",
        "instance_name": "Business"
      },
      "avatar_url": "",
      "channel_id": 24,
      "greeting_enabled": false,
      "working_hours_enabled": false,
      "enable_email_collect": true,
      "csat_survey_enabled": false,
      "enable_auto_assignment": true,
      "allow_messages_after_resolved": true,
      "timezone": "UTC"
    },
    {
      "id": 62,
      "name": "Flouds",
      "channel_type": "Channel::Api",
      "webhook_url": "https://evo.flouds.com.br/chatwoot/webhook/Flouds",
      "inbox_identifier": "uQFCmgZC9WxYHR8P8dri5LEG",
      "hmac_token": "CCBC5YFvYUGybzA7tSbBbRBe"
    }
  ]
}
```

**Campos importantes para integração:**
- ✅ **id**: ID numérico do inbox (usado nos payloads)
- ✅ **name**: Nome do inbox (deve fazer match com `name_inbox` do banco)
- ✅ **channel_type**: Tipo do canal (`Channel::Whatsapp`, `Channel::Api`, etc)
- ✅ **phone_number**: Número do WhatsApp (se aplicável)
- ✅ **callback_webhook_url**: URL atual do webhook configurado

### 🎯 Como Encontrar o Inbox Correto

**Lógica de Match:**
1. Listar todos os inboxes via API
2. Comparar `inbox.name` com `config.name_inbox` do banco
3. Usar `inbox.id` nos payloads de criação de contatos/conversas

**Exemplo de código:**
```go
// Buscar inbox pelo nome configurado no banco
func (c *Client) GetInboxByName(name string) (*Inbox, error) {
    inboxes, err := c.ListInboxes()
    if err != nil {
        return nil, err
    }
    
    for _, inbox := range inboxes {
        if inbox.Name == name { // Match exato
            return &inbox, nil
        }
    }
    
    return nil, nil // Não encontrado
}
```

---

## 📱 Processamento de Mensagens FromMe

### 🔄 Lógica Implementada (Compatível com chatwoot-lib)

**Comportamento correto:**
```go
// Determinar message_type baseado em IsFromMe
messageType := 0 // 0 = incoming (default)
if evt.Info.IsFromMe {
    messageType = 1 // 1 = outgoing
}
```

**Fluxo de processamento:**
1. ✅ **Mensagem enviada** pelo usuário no celular (`fromMe=true`)
   - → Processada pelo wuzapi
   - → Enviada para Chatwoot como `message_type: 1` (outgoing)
   - → Aparece no painel como mensagem **enviada**

2. ✅ **Mensagem recebida** de contato (`fromMe=false`)
   - → Processada pelo wuzapi
   - → Enviada para Chatwoot como `message_type: 0` (incoming)
   - → Aparece no painel como mensagem **recebida**

**Resultado final:**
- 🎯 **Operador vê histórico completo** da conversa
- 🎯 **Mensagens ordenadas cronologicamente**
- 🎯 **Diferenciação visual** entre enviadas/recebidas
- 🎯 **Compatibilidade total** com chatwoot-lib

### ❌ Implementação Anterior (Incorreta)

**Problema identificado:**
```go
// ESTAVA IGNORANDO mensagens fromMe
if evt.Info.IsFromMe {
    return true // ❌ INCORRETO
}
```

**Resultado problemático:**
- ❌ Mensagens enviadas pelo celular **não apareciam** no Chatwoot
- ❌ Operador só via **metade da conversa**
- ❌ Histórico incompleto e confuso

### ✅ Correção Aplicada

**Código atualizado:**
```go
// shouldIgnoreMessage - REMOVIDO filtro IsFromMe
func shouldIgnoreMessage(config *Config, evt *events.Message) bool {
    // 1. Verificar grupos
    if evt.Info.Chat.Server == "g.us" && isGroupIgnored(config) {
        return true
    }
    
    // 2. Verificar JIDs ignorados
    ignoreList := parseIgnoreJids(config.IgnoreJids)
    for _, jid := range ignoreList {
        if evt.Info.Chat.String() == jid {
            return true
        }
    }
    
    // 3. REMOVIDO: Não ignorar mensagens fromMe
    // Mensagens fromMe são enviadas como "outgoing"
    
    return false
}
```

**Funções atualizadas:**
- `sendTextMessage()` - Aceita parâmetro `messageType`
- `sendTextMessageWithRetry()` - Propaga `messageType`
- Logs mostram tipo correto (incoming/outgoing)

**Correção adicional - Contato correto:**
```go
// CORRIGIDO: Sempre usar evt.Info.Chat.User (não Sender.User)
phone := evt.Info.Chat.User

// Chat.User = SEMPRE o "outro lado" da conversa
// - Para fromMe=true: Chat.User = destinatário
// - Para fromMe=false: Chat.User = remetente (mesmo valor)
// - Resultado: conversa sempre no contato correto
```

---

## ⚠️ Pontos de Atenção

### 🔴 Inconsistências Identificadas

1. **IDs em Conversas**: 
   - Método padrão usa strings: `"contact_id": "456"`
   - Método com source_id usa números: `"contact_id": 456`

2. **Phone Numbers**:
   - Busca: SEM + (`"5511999999999"`)
   - Criação: COM + (`"+5511999999999"`)
   - Identifier: SEMPRE SEM + (`"5511999999999@s.whatsapp.net"`)

### 🟡 Campos Opcionais vs Obrigatórios

| Campo | Busca | Criação | Mensagem | Status |
|---|---|---|---|---|
| `phone_number` | ✅ | ✅ | ❌ | ❌ |
| `identifier` | ✅ | ✅ | ❌ | ❌ |
| `contact_id` | ❌ | ❌ | ❌ | ❌ |
| `inbox_id` | ❌ | ✅ | ❌ | ❌ |
| `content` | ❌ | ❌ | ✅ | ❌ |
| `message_type` | ❌ | ❌ | ✅ | ❌ |
| `source_id` | ❌ | ❌ | 🟡 | ❌ |
| `status` | ❌ | ❌ | ❌ | ✅ |

**Legenda**: ✅ Obrigatório | 🟡 Opcional | ❌ Não usado

---

## 📝 Observações da Implementação

### ✅ Boas Práticas Seguidas
- Logs detalhados para debugging de payloads
- Validação de dados antes de enviar
- Tratamento de erros da API
- Cache para evitar requisições desnecessárias
- Normalização de números de telefone
- **Processamento correto de mensagens fromMe** (compatível com chatwoot-lib)
- **Histórico completo** de conversas no painel Chatwoot

### 🔄 Melhorias Sugeridas
1. **Padronizar IDs**: Definir se usamos strings ou números consistentemente
2. **Validação Payload**: Validar estrutura antes de enviar
3. ✅ **~~Retry Logic~~**: ✅ **IMPLEMENTADO** - Retry para falhas temporárias (404)
4. **Rate Limiting**: Respeitar limites da API Chatwoot
5. ✅ **~~Match Inbox~~**: ✅ **IMPLEMENTADO** - Busca robusta por nome do inbox
6. ✅ **~~Cache Inbox ID~~**: ✅ **IMPLEMENTADO** - Cache do ID do inbox
7. ✅ **~~FromMe Messages~~**: ✅ **IMPLEMENTADO** - Processamento correto de mensagens fromMe

---

## 🔄 Tratamento de Identificadores - Resumo Técnico

### 📋 Fluxo de Identificação por Tipo

**1. Conversas Individuais:**
```go
// Sempre usar Chat.User para identificação
phone := evt.Info.Chat.User  // Ex: "5511999999999"

// Busca no Chatwoot
phone_number: "5511999999999" (sem +)
identifier: "5511999999999@s.whatsapp.net"

// Criação no Chatwoot  
phone_number: "+5511999999999" (com +)
identifier: "5511999999999@s.whatsapp.net" (sem +)

// Conteúdo da mensagem
content: "Texto original da mensagem"
```

**2. Grupos (como contato único):**
```go
// Sempre usar Chat.User para identificação do grupo
groupPhone := evt.Info.Chat.User  // Ex: "120363123456789012"

// Busca no Chatwoot
phone_number: "120363123456789012" (ID do grupo)
identifier: "120363123456789012@g.us"

// Criação no Chatwoot
phone_number: "120363123456789012" (sem +, ID do grupo)
identifier: "120363123456789012@g.us"
name: "Nome do Grupo (GROUP)"

// Conteúdo da mensagem com identificação
content: "[+55 (11) 99999-9999 - João Silva]: Texto da mensagem"
// ou para bot:
content: "[Bot]: Mensagem enviada"
```

### 🎯 Pontos-Chave da Implementação

1. **Identificador único consistente**: `evt.Info.Chat.User` sempre
2. **Diferenciação por servidor**: `@s.whatsapp.net` vs `@g.us`  
3. **Formatação de números**: + para criação, sem + para busca
4. **Contexto em grupos**: Remetente identificado no conteúdo
5. **Cache unificado**: Mesmo sistema para individual e grupo
6. **Tratamento no Chatwoot**: Grupos como contatos especiais

---

**📅 Documento atualizado em:** 2025-08-25  
**🔧 Versão da integração:** v2.3 - Individual vs Group Identification  
**📋 Status:** Payloads + FromMe Messages + Identificadores Individual/Grupo documentados