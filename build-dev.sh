#!/bin/bash

# Script para build da versão de desenvolvimento do wuzapi
# Versão: 2.0 - Com suporte completo ao Chatwoot

echo "🚀 Building wuzapi:dev image..."

# Build da imagem Docker
docker build -t wuzapi:dev .

if [ $? -eq 0 ]; then
    echo "✅ Build completed successfully!"
    echo ""
    echo "📋 Next steps:"
    echo "1. Run the container:"
    echo "   docker run -d --name wuzapi-dev -p 8080:8080 wuzapi:dev"
    echo ""
    echo "2. Access the dashboard:"
    echo "   http://localhost:8080/dashboard"
    echo ""
    echo "3. Configure Chatwoot:"
    echo "   - Click on 'Chatwoot Integration' card"
    echo "   - Fill in your Chatwoot credentials"
    echo "   - Test the connection"
    echo "   - Save configuration"
    echo ""
    echo "4. Test integration:"
    echo "   - Send a text message to your WhatsApp"
    echo "   - Check if it appears in Chatwoot"
    echo ""
    echo "🔍 Monitor logs:"
    echo "   docker logs -f wuzapi-dev"
else
    echo "❌ Build failed!"
    exit 1
fi