# Wuzapi + Chatwoot Integration

## Status: ✅ 100% COMPLETO E FUNCIONAL

**Arquitetura:** Package Go nativo (`chatwoot/`) integrado ao wuzapi para atendimento WhatsApp via Chatwoot.

## Releases
- **GitHub:** [v1.1.1](https://github.com/flouds-dncarbonell/wuzapi/tree/v1.1.1)
- **Docker:** `dncarbonell/wuzapi:v1.1.1` / `dncarbonell/wuzapi:latest`

## Como Usar
1. Acesse `http://localhost:8080/dashboard`
2. Configure Chatwoot (URL, Account ID, Token)
3. Teste conexão e ative integração
4. Fluxo bidirecional automático

## Arquitetura
```
chatwoot/
├── models.go      # Config, structs + DB functions
├── client.go      # HTTP client Chatwoot API
├── handlers.go    # REST API endpoints
├── cache.go       # Cache com TTL
├── processor.go   # WhatsApp → Chatwoot
├── webhook.go     # Chatwoot → WhatsApp
├── media.go       # Mídias + stickers WebP
├── messages.go    # Quotes + tracking
└── chatwoot.go    # Entry point
```

## Funcionalidades
- ✅ **Fluxo Bidirecional:** WhatsApp ↔ Chatwoot completo
- ✅ **Mídias:** Imagens, vídeos, áudios, documentos, stickers
- ✅ **Quotes:** Sistema bidirecional com database
- ✅ **Message Deletion:** Sincronização automática
- ✅ **Bot Commands:** `/init`, `/status`, `/clearcache`, `/disconnect`
- ✅ **Validação WhatsApp:** Números inválidos detectados
- ✅ **Anti-Loop + Cache:** Performance e confiabilidade
- ✅ **Dashboard:** Interface web completa

## API REST
```bash
# Configurar
curl -X POST http://localhost:8080/chatwoot/config \
  -H "token: TOKEN" -H "Content-Type: application/json" \
  -d '{"enabled":true,"account_id":"123","token":"chatwoot_token","url":"https://app.chatwoot.com"}'

# Status
curl -X GET http://localhost:8080/chatwoot/status -H "token: TOKEN"
```

## Docker
```bash
# Usar versão oficial
docker pull dncarbonell/wuzapi:v1.1.1

# Executar
docker run -d -p 8080:8080 -e DB_TYPE=sqlite -v $(pwd)/data:/app/data dncarbonell/wuzapi:v1.1.1
```

---
*Versão v1.1.1 - Release estável com todas as funcionalidades ativas*