#!/usr/bin/env bash
# GoBackup Agent — instalador automático (Docker)
#
# Uso recomendado (um comando só, numa máquina nova):
#
#   curl -fsSL https://raw.githubusercontent.com/cleberreissosdocs/gobackup/main/scripts/install-agent.sh | bash
#
# Ou, se você já clonou o repositório:
#
#   ./scripts/install-agent.sh
#
# O script é seguro para rodar de novo (idempotente): se já existir uma
# instalação em INSTALL_DIR, ele reaproveita o .env.agent existente (nunca
# gera uma senha nova nem sobrescreve uma instalação que já está rodando) e
# só garante que os containers estejam no ar e atualizados.
#
# Variáveis de ambiente que você pode definir ANTES de rodar para customizar
# sem interação (útil para automação/CI — todas têm um valor padrão sensato):
#   INSTALL_DIR          Pasta onde o agente será instalado (padrão: ~/gobackup-agent)
#   AGENT_HOSTNAME        Nome fixo deste agente (padrão: hostname -s da máquina)
#   AGENT_WEB_PORT        Porta da interface web (padrão: 61081)
#   BACKUP_SOURCE_DIR     Pasta do host exposta em /backups no container (padrão: INSTALL_DIR/backups)
#   GOBACKUP_REPO_URL     URL do repositório Git (padrão: o GoBackup oficial)
#   GOBACKUP_REF          Branch/tag a usar (padrão: main)
#
# Senha do banco de dados: gerada automaticamente com openssl/urandom (32
# bytes aleatórios, nunca uma senha fixa ou previsível), gravada apenas no
# arquivo .env.agent com permissão 600 (leitura/escrita só pelo dono) — não é
# impressa no terminal nem fica em nenhum histórico de comando.

set -euo pipefail

# ---------------------------------------------------------------------------
# Config (todas sobrescrevíveis por variável de ambiente)
# ---------------------------------------------------------------------------
INSTALL_DIR="${INSTALL_DIR:-$HOME/gobackup-agent}"
AGENT_HOSTNAME="${AGENT_HOSTNAME:-$(hostname -s 2>/dev/null || hostname)}"
AGENT_WEB_PORT="${AGENT_WEB_PORT:-61081}"
BACKUP_SOURCE_DIR="${BACKUP_SOURCE_DIR:-$INSTALL_DIR/backups}"
GOBACKUP_REPO_URL="${GOBACKUP_REPO_URL:-https://github.com/cleberreissosdocs/gobackup.git}"
GOBACKUP_REF="${GOBACKUP_REF:-main}"

COMPOSE_FILE="docker-compose.agent.yml"
ENV_FILE=".env.agent"

# ---------------------------------------------------------------------------
# Saída bonitinha
# ---------------------------------------------------------------------------
C_BLUE="\033[1;34m"; C_GREEN="\033[1;32m"; C_YELLOW="\033[1;33m"; C_RED="\033[1;31m"; C_RESET="\033[0m"
step() { printf "\n${C_BLUE}▶ %s${C_RESET}\n" "$1"; }
ok()   { printf "${C_GREEN}✓ %s${C_RESET}\n" "$1"; }
warn() { printf "${C_YELLOW}! %s${C_RESET}\n" "$1"; }
die()  { printf "${C_RED}✗ %s${C_RESET}\n" "$1" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 1. Pré-requisitos
# ---------------------------------------------------------------------------
step "Verificando pré-requisitos"

if ! command -v docker >/dev/null 2>&1; then
  warn "Docker não encontrado — instalando via script oficial (get.docker.com)..."
  curl -fsSL https://get.docker.com | sh
  if ! command -v docker >/dev/null 2>&1; then
    die "Falha ao instalar o Docker automaticamente. Instale manualmente: https://docs.docker.com/engine/install/"
  fi
  if [ "${EUID:-$(id -u)}" -ne 0 ] && ! groups "$USER" 2>/dev/null | grep -qw docker; then
    sudo usermod -aG docker "$USER" || true
    warn "Seu usuário foi adicionado ao grupo 'docker'. Pode ser necessário sair e entrar de novo na sessão SSH para isso valer (ou rode o resto deste script com 'sudo')."
  fi
  ok "Docker instalado"
else
  ok "Docker já instalado ($(docker --version))"
fi

DOCKER_COMPOSE_CMD=""
if docker compose version >/dev/null 2>&1; then
  DOCKER_COMPOSE_CMD="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  DOCKER_COMPOSE_CMD="docker-compose"
else
  warn "Plugin 'docker compose' não encontrado — instalando para o usuário atual (sem precisar de root)..."
  mkdir -p "$HOME/.docker/cli-plugins"
  ARCH="$(uname -m)"
  curl -fsSL -o "$HOME/.docker/cli-plugins/docker-compose" \
    "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-${ARCH}"
  chmod +x "$HOME/.docker/cli-plugins/docker-compose"
  if docker compose version >/dev/null 2>&1; then
    DOCKER_COMPOSE_CMD="docker compose"
  else
    die "Não foi possível obter o Docker Compose automaticamente. Instale manualmente e rode este script de novo."
  fi
fi
ok "Docker Compose disponível ($DOCKER_COMPOSE_CMD)"

if ! command -v openssl >/dev/null 2>&1 && [ ! -r /dev/urandom ]; then
  die "Preciso de 'openssl' ou /dev/urandom para gerar a senha do banco com segurança. Instale openssl e rode de novo."
fi

if ss -tln 2>/dev/null | grep -q ":${AGENT_WEB_PORT}\b"; then
  die "A porta ${AGENT_WEB_PORT} já está em uso neste servidor. Defina outra com AGENT_WEB_PORT=porta antes de rodar o script."
fi

# ---------------------------------------------------------------------------
# 2. Baixar os arquivos
# ---------------------------------------------------------------------------
step "Preparando os arquivos em $INSTALL_DIR"

if [ -f "$INSTALL_DIR/$COMPOSE_FILE" ]; then
  ok "Instalação já existe em $INSTALL_DIR — reaproveitando (nada será sobrescrito)"
else
  if [ -d "$INSTALL_DIR/.git" ]; then
    ok "Repositório já clonado em $INSTALL_DIR"
  else
    mkdir -p "$INSTALL_DIR"
    if [ -n "$(ls -A "$INSTALL_DIR" 2>/dev/null)" ]; then
      die "$INSTALL_DIR já existe e não está vazio, mas não contém uma instalação do GoBackup. Escolha outra pasta com INSTALL_DIR=... ou limpe-a antes."
    fi
    command -v git >/dev/null 2>&1 || die "git não encontrado — instale-o (apt-get install -y git) e rode de novo."
    git clone --branch "$GOBACKUP_REF" --depth 1 "$GOBACKUP_REPO_URL" "$INSTALL_DIR"
  fi
  ok "Código baixado"
fi

cd "$INSTALL_DIR"

# ---------------------------------------------------------------------------
# 3. Gerar .env.agent com senha forte (só na primeira instalação)
# ---------------------------------------------------------------------------
step "Configurando $ENV_FILE"

if [ -f "$ENV_FILE" ]; then
  ok "$ENV_FILE já existe — mantendo a configuração atual (senha não é alterada)"
else
  if command -v openssl >/dev/null 2>&1; then
    DB_PASSWORD="$(openssl rand -base64 32 | tr -d '\n=+/' | cut -c1-32)"
  else
    DB_PASSWORD="$(head -c 48 /dev/urandom | base64 | tr -d '\n=+/' | cut -c1-32)"
  fi
  [ -n "$DB_PASSWORD" ] || die "Falha ao gerar a senha do banco de dados."

  mkdir -p "$BACKUP_SOURCE_DIR" "$INSTALL_DIR/ssh-keys"

  cat > "$ENV_FILE" <<EOF
# Gerado automaticamente por install-agent.sh em $(date -Iseconds)
# Senha forte, única por instalação — não reutilize em outro servidor.
POSTGRES_PASSWORD=${DB_PASSWORD}
AGENT_WEB_PORT=${AGENT_WEB_PORT}
HEARTBEAT_INTERVAL=1s
AGENT_HOSTNAME=${AGENT_HOSTNAME}
BACKUP_SOURCE_DIR=${BACKUP_SOURCE_DIR}
SSH_KEY_DIR=${INSTALL_DIR}/ssh-keys
MNT_HOST_DIR=/mnt
EOF
  chmod 600 "$ENV_FILE"
  unset DB_PASSWORD
  ok "Senha do banco de dados gerada (32 caracteres aleatórios) e gravada em $ENV_FILE com permissão 600 — nunca exibida no terminal"
fi

# ---------------------------------------------------------------------------
# 4. Subir o banco, migrar, subir o agente
# ---------------------------------------------------------------------------
step "Subindo o banco de dados (Postgres)"
$DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d --build postgres

printf "Aguardando o banco ficar saudável"
status=""
for _ in $(seq 1 30); do
  cid="$($DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps -q postgres 2>/dev/null || true)"
  if [ -n "$cid" ]; then
    status="$(docker inspect --format '{{.State.Health.Status}}' "$cid" 2>/dev/null || true)"
  fi
  if [ "$status" = "healthy" ]; then
    printf "\n"
    break
  fi
  printf "."
  sleep 2
done
[ "$status" = "healthy" ] || die "O banco de dados não ficou saudável a tempo. Rode '$DOCKER_COMPOSE_CMD -f $COMPOSE_FILE logs postgres' para investigar."
ok "Banco de dados no ar"

step "Aplicando as migrações do banco"
PG_PASSWORD="$(grep -m1 '^POSTGRES_PASSWORD=' "$ENV_FILE" | cut -d= -f2-)"
$DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --env-file "$ENV_FILE" run --rm agent \
  migrate -db "postgres://gobackup:${PG_PASSWORD}@postgres:5432/gobackup?sslmode=disable"
unset PG_PASSWORD
ok "Migrações aplicadas"

step "Subindo o agente"
$DOCKER_COMPOSE_CMD -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d --build
ok "Agente no ar"

# ---------------------------------------------------------------------------
# 5. Resumo final
# ---------------------------------------------------------------------------
HOST_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$HOST_IP" ] || HOST_IP="<ip-deste-servidor>"

cat <<EOF

${C_GREEN}=============================================================${C_RESET}
 GoBackup Agente instalado com sucesso!
${C_GREEN}=============================================================${C_RESET}

  Acesse:     https://${HOST_IP}:${AGENT_WEB_PORT}/
  Usuário:    admin
  Senha:      admin   (troque em "Usuários" assim que entrar)

  Pasta de instalação: ${INSTALL_DIR}
  Pasta de backups:    ${BACKUP_SOURCE_DIR}

  Próximo passo: faça login e, em "Configurações", aponte este agente
  para o endereço do seu painel Central.

  Comandos úteis:
    cd ${INSTALL_DIR}
    ${DOCKER_COMPOSE_CMD} -f ${COMPOSE_FILE} --env-file ${ENV_FILE} logs -f agent
    ${DOCKER_COMPOSE_CMD} -f ${COMPOSE_FILE} --env-file ${ENV_FILE} restart agent

EOF
