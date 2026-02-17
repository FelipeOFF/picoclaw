# PicoClaw Systemd Service

Este documento explica como instalar o PicoClaw Gateway como um serviço systemd para rodar automaticamente em background.

## 📋 Requisitos

- Linux com systemd (Ubuntu, Debian, Fedora, Arch, etc.)
- PicoClaw instalado e configurado
- Acesso sudo (para instalar o serviço)

## 🚀 Instalação Rápida

```bash
# Navegue até o diretório do picoclaw
cd ~/picoclaw-git

# Execute o script de instalação
./install-systemd-service.sh
```

Isso irá:
1. Detectar automaticamente o binário do picoclaw
2. Criar o arquivo de serviço systemd
3. Iniciar o serviço
4. Habilitar início automático no boot

## 📖 Comandos Disponíveis

### Instalar e Iniciar
```bash
./install-systemd-service.sh install
```

### Ver Status
```bash
./install-systemd-service.sh status
```

### Ver Logs em Tempo Real
```bash
./install-systemd-service.sh logs
```

### Reiniciar (após mudar configuração)
```bash
./install-systemd-service.sh restart
```

### Parar o Serviço
```bash
./install-systemd-service.sh stop
```

### Desinstalar
```bash
./install-systemd-service.sh uninstall
```

## ⚙️ Configuração

Antes de instalar, edite seu arquivo de configuração:

```bash
nano ~/.picoclaw/config.json
```

Exemplo de configuração com Kimi CLI e Telegram:

```json
{
  "agents": {
    "defaults": {
      "provider": "kimi-cli",
      "model": "kimi-cli"
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "SEU_TOKEN_AQUI",
      "allow_from": ["SEU_USER_ID"]
    }
  }
}
```

Depois de editar, reinicie o serviço:

```bash
./install-systemd-service.sh restart
```

## 🔧 Comandos Systemd Diretos

Se preferir usar o systemd diretamente:

```bash
# Ver status
sudo systemctl status picoclaw-gateway

# Iniciar
sudo systemctl start picoclaw-gateway

# Parar
sudo systemctl stop picoclaw-gateway

# Reiniciar
sudo systemctl restart picoclaw-gateway

# Ver logs
sudo journalctl -u picoclaw-gateway -f

# Habilitar início automático
sudo systemctl enable picoclaw-gateway

# Desabilitar início automático
sudo systemctl disable picoclaw-gateway
```

## 📁 Arquivos

- **Serviço**: `/etc/systemd/system/picoclaw-gateway.service`
- **Configuração**: `~/.picoclaw/config.json`
- **Workspace**: `~/.picoclaw/workspace/`
- **Logs**: `sudo journalctl -u picoclaw-gateway`

## 🐛 Troubleshooting

### Serviço não inicia

1. Verifique se o picoclaw está no PATH:
   ```bash
   which picoclaw
   ```

2. Verifique os logs:
   ```bash
   ./install-systemd-service.sh logs
   ```

3. Teste manualmente:
   ```bash
   picoclaw gateway
   ```

### Configuração não encontrada

O script cria uma configuração padrão se não existir. Edite-a em:
```bash
~/.picoclaw/config.json
```

### Permissão negada

Certifique-se de ter acesso sudo:
```bash
sudo whoami
```

## 📝 Autostart no Boot

O serviço é habilitado automaticamente para iniciar no boot. Para desabilitar:

```bash
sudo systemctl disable picoclaw-gateway
```

## 🔄 Atualização

Se atualizar o PicoClaw, basta reiniciar o serviço:

```bash
./install-systemd-service.sh restart
```

---

**Nota**: Este script funciona para qualquer usuário, detectando automaticamente o diretório home e as configurações.
