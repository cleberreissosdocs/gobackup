# GoBackup

Sistema de backup para servidores Linux, com um agente local (administração
de backups de arquivos e bancos de dados) e um painel central, hospedado em
infraestrutura separada, que monitora a saúde dos backups de todos os
servidores e também pode administrá-los remotamente.

Ver `DESIGN.md` para o desenho técnico completo (modelo de dados, protocolo,
decisões de arquitetura). Este README é o guia prático de build/deploy/uso.

## Tipos de backup suportados

- **Arquivos** (via `rsync`) — completo, incremental ou diferencial, com
  destino local, NAS (pasta de rede) ou SSH (outro servidor).
- **Banco de dados** — MariaDB, MySQL, PostgreSQL, SQLite e SQL Server / SQL
  Server Express. Pode fazer backup de um banco específico (ou de todos de
  uma vez, exceto SQL Server — ver observação abaixo) com compressão
  opcional (gzip, zip ou nenhuma).

  > SQL Server usa seu próprio backup nativo (`BACKUP DATABASE`), que sempre
  > escreve no disco do próprio servidor — por isso o agente GoBackup
  > precisa rodar na MESMA máquina da instância SQL Server/SQL Server
  > Express (o caso comum). Requer `sqlcmd` instalado e no PATH — Windows:
  > [baixe aqui](https://github.com/microsoft/go-sqlcmd/releases) ou use o
  > `sqlcmd.exe` que já vem com o SQL Server Command Line Utilities; Linux:
  > já incluído na imagem Docker do agente. Não suporta "todos os bancos"
  > num único arquivo — crie uma tarefa por banco.

Ambos criados e disparados pela interface web (local do agente ou
remotamente pelo Central) ou pela CLI — veja a seção **Uso** mais abaixo
para os comandos.

## Instalação rápida (recomendado)

### Central (painel)

Hospedado em infraestrutura separada (seu próprio servidor/VM com Docker).
Resumo rápido — detalhes completos na seção **Deploy com Docker** mais
abaixo:

```bash
git clone https://github.com/cleberreissosdocs/gobackup.git && cd gobackup
cp .env.central.example .env.central
# edite POSTGRES_PASSWORD e TLS_HOSTS (IP/hostname que os agentes vão usar)

docker compose -f docker-compose.central.yml --env-file .env.central up -d --build

# aplicar o schema do banco (passo único, só na 1ª instalação):
docker compose -f docker-compose.central.yml --env-file .env.central exec central \
  gobackup-central migrate -db "postgres://gobackup:***@postgres:5432/gobackup?sslmode=disable"
```

Acesse `https://<host>:61443/` e entre com `admin` / `admin` (criado
automaticamente na primeira subida) — troque a senha assim que entrar, em
**Usuários**.

### Agente Linux

Pra instalar o **gobackup-agent** num servidor Linux novo, existe um
instalador automático: baixa o Docker se precisar, baixa o código, gera uma
senha de banco de dados forte e única, sobe tudo e deixa pronto pro
primeiro login — tudo com um único comando:

```bash
curl -fsSL https://raw.githubusercontent.com/cleberreissosdocs/gobackup/main/scripts/install-agent.sh | bash
```

Ao final, o próprio script mostra o endereço (`https://<ip-do-servidor>:61081/`)
e o login padrão (`admin` / `admin` — troque assim que entrar, em
**Usuários**). É seguro reexecutar o mesmo comando depois (ex. pra
atualizar): ele detecta a instalação existente e não mexe na senha do
banco nem apaga nada. Depois de instalado, abra **Configurações** na UI do
agente e aponte-o para o endereço do Central de cima.

Personalização opcional (defina a variável antes do `curl`):

| Variável | Para que serve | Padrão |
|---|---|---|
| `AGENT_HOSTNAME` | Nome fixo deste agente no painel Central | nome do servidor |
| `AGENT_WEB_PORT` | Porta da interface web | `61081` |
| `INSTALL_DIR` | Pasta onde instalar | `~/gobackup-agent` |
| `BACKUP_SOURCE_DIR` | Pasta usada para os backups | `INSTALL_DIR/backups` |

```bash
AGENT_HOSTNAME=srv-financeiro AGENT_WEB_PORT=61082 curl -fsSL \
  https://raw.githubusercontent.com/cleberreissosdocs/gobackup/main/scripts/install-agent.sh \
  | bash
```

Manuais em PDF, passo a passo com capturas de tela:
[instalador automático](docs/manuais/GoBackup-Manual-Script-Instalacao-Automatica.pdf) ·
[instalação manual via Docker](docs/manuais/GoBackup-Manual-Instalacao-Agente-Linux-Docker.pdf)

### Agente Windows

Baixe o instalador (Windows 10/11, 64 bits):
[**gobackup-agent-setup.exe**](https://github.com/cleberreissosdocs/gobackup/releases/download/windows-agent-v1/gobackup-agent-setup.exe)

Dê duplo clique, siga o assistente (o Windows pode avisar via SmartScreen
na primeira execução — clique em "Mais informações" → "Executar assim
mesmo", é normal para um instalador interno sem assinatura paga). Ao
terminar, o GoBackup fica rodando como ícone na bandeja do sistema e já
inicia sozinho com o Windows.

Clique com o botão direito no ícone → **Configurações** (abre no
navegador) e entre com a senha padrão `admin` (troque em seguida, tem
botão próprio pra isso). Lá você informa o endereço do Central de cima e
clica em "Registrar" — o ícone fica verde assim que conectar.

Manual completo com screenshots de cada tela:
[`docs/manuais/GoBackup-Manual-Instalacao-Agente-Windows.pdf`](docs/manuais/GoBackup-Manual-Instalacao-Agente-Windows.pdf) —
ou gere o instalador você mesmo a partir do código-fonte
(`installer/gobackup-agent.nsi`, precisa de `makensis` e dos binários
`cmd/winagent` e `cmd/winagent-tray` cross-compilados para Windows).

Prefere instalar manualmente (Docker passo a passo, ou nativo sem Docker)?
Veja as seções **Deploy com Docker** e **Deploy nativo** mais abaixo.

## Componentes

- **gobackup-agent** — instalado em cada servidor a proteger. Orquestra
  `rsync` (backup de arquivos) e `mysqldump`/`pg_dump`/`pg_dumpall`/`sqlcmd`/cópia
  de arquivo (backup de banco: MariaDB, MySQL, PostgreSQL, SQLite, SQL
  Server). Tem sua própria interface web de administração e seu próprio
  Postgres local.
- **gobackup-central** — hospedado em infraestrutura separada. Painel web
  com status de todos os agentes (online/offline, última execução), e pode
  administrar qualquer agente remotamente com as mesmas opções da UI local
  (criar/rodar backup, navegar diretórios).

Comunicação: o agente SEMPRE inicia a conexão com o Central (polling HTTPS
a cada 1s, por padrão) — funciona atrás de NAT/firewall sem abrir porta
nenhuma no servidor protegido. O mesmo ciclo carrega heartbeat, comandos
remotos e os resultados desses comandos. Com esse intervalo curto, o
Central detecta um agente offline em ~5s de silêncio (antes eram 45s).

## Segurança

- **TLS obrigatório por padrão** em ambas as aplicações. Sem `-tls-cert`/
  `-tls-key`, cada uma gera um certificado autoassinado na primeira
  execução (persistido em disco). O agente verifica o certificado do
  Central via CA pinado (`-central-ca`) — não há bypass de verificação.
- **Login com bcrypt** + cookies `HttpOnly`, `Secure` (quando TLS ativo) e
  `SameSite=Lax`.
- **CSRF** em todo formulário que muda estado (criar job, criar canal de
  notificação), com token por sessão validado em tempo constante.
- **Rate limiting de login**: 5 tentativas erradas bloqueiam o e-mail por 1
  minuto.
- **Endpoints administrativos** (`/api/v1/notification-channels`,
  `/api/v1/agents/{id}/commands`) exigem sessão de login.
- Senhas de banco de dados usadas nos backups nunca são persistidas em
  texto puro (só passam no payload do comando, via variável de ambiente do
  subprocesso — `PGPASSWORD`/`MYSQL_PWD`).

## Deploy com Docker (manual, passo a passo)

### Central

```bash
cp .env.central.example .env.central
# edite POSTGRES_PASSWORD e TLS_HOSTS (deve incluir o IP/hostname real que
# os agentes vão usar para conectar)

docker compose -f docker-compose.central.yml --env-file .env.central up -d --build

# aplicar schema (usuário admin/admin é criado automaticamente no 1º start):
docker compose -f docker-compose.central.yml --env-file .env.central exec central \
  gobackup-central migrate -db "postgres://gobackup:***@postgres:5432/gobackup?sslmode=disable"

# copiar o certificado para distribuir aos agentes:
docker compose -f docker-compose.central.yml --env-file .env.central cp \
  central:/data/gobackup-central-cert.pem ./gobackup-central-cert.pem
```

Acesse `https://<host>:61443/` (aceite o certificado autoassinado no
navegador, ou instale-o como CA confiável) e entre com `admin` / `admin` —
troque a senha assim que entrar, em **Usuários**.

### Agent (em cada servidor a proteger)

```bash
cp .env.agent.example .env.agent
# edite POSTGRES_PASSWORD, BACKUP_SOURCE_DIR (diretório do host que será
# exposto em /backups dentro do container — é o que você digita como
# origem/destino na UI), SSH_KEY_DIR se for usar destino SSH.
# Não precisa mais definir a URL do Central aqui — isso é feito depois,
# pela própria interface web.

docker compose -f docker-compose.agent.yml --env-file .env.agent up -d --build postgres

# aplicar schema (usuário admin/admin é criado automaticamente no 1º start):
docker compose -f docker-compose.agent.yml --env-file .env.agent run --rm agent \
  migrate -db "postgres://gobackup:***@postgres:5432/gobackup?sslmode=disable"

docker compose -f docker-compose.agent.yml --env-file .env.agent up -d
```

Isso sobe dois containers: `postgres` (Postgres local do agente) e `agent`
(UI web em `https://<host>:61081/`, com o heartbeat para o Central rodando
embutido no mesmo processo).

Depois, abra `https://<host>:61081/`, faça login com `admin` / `admin`
(troque a senha assim que entrar, em **Usuários**), e vá em
**Configurações** (link no topo). Lá você:
- Dá um nome para este agente (como vai aparecer no painel do Central).
- Informa a URL do Central (ex: `https://192.168.122.151:61443`).
- Cola o conteúdo do certificado CA do Central (o mesmo
  `gobackup-central-cert.pem` copiado no passo do Central acima) — deixe
  em branco se o Central já tiver um certificado de CA pública confiável.
- Clica em "Registrar e conectar". Isso gera um token novo no Central e o
  heartbeat (rodando dentro do mesmo processo da UI) já passa a usar essa
  configuração no próximo ciclo — sem precisar reiniciar nada.

Caminhos usados na UI/CLI (origem/destino) devem ser os caminhos DENTRO do
container — ex. `/backups/meusite` para o que está montado em
`${BACKUP_SOURCE_DIR}/meusite` no host.

## Deploy nativo (sem Docker)

```bash
# build
CGO_ENABLED=0 GOOS=linux go build -o gobackup-agent ./cmd/agent
CGO_ENABLED=0 GOOS=linux go build -o gobackup-central ./cmd/central

# no servidor do Central
sudo apt-get install -y postgresql
sudo -u postgres psql -c "CREATE ROLE gobackup LOGIN PASSWORD 'senha';"
sudo -u postgres psql -c "CREATE DATABASE gobackup OWNER gobackup;"
./gobackup-central migrate -db "postgres://gobackup:***@127.0.0.1:5432/gobackup?sslmode=disable"
# "serve" cria o usuário admin/admin automaticamente no 1º start (troque a
# senha depois, em Usuários) — não precisa rodar create-user manualmente.
./gobackup-central serve -db "postgres://gobackup:***@127.0.0.1:5432/gobackup?sslmode=disable" \
  -addr :61443 -tls-hosts "<ip-do-central>,localhost,127.0.0.1"
# copie o gobackup-central-cert.pem gerado para cada agente

# em cada servidor protegido
sudo apt-get install -y postgresql rsync
sudo -u postgres psql -c "CREATE ROLE gobackup LOGIN PASSWORD 'senha';"
sudo -u postgres psql -c "CREATE DATABASE gobackup OWNER gobackup;"
./gobackup-agent migrate -db "postgres://gobackup:***@127.0.0.1:5432/gobackup?sslmode=disable"
./gobackup-agent register -central https://<central-host>:61443 -name <nome> \
  -token-file /etc/gobackup/agent-token -central-ca ./gobackup-central-cert.pem
./gobackup-agent serve -db "postgres://gobackup:***@127.0.0.1:5432/gobackup?sslmode=disable" -addr :61081 &
# login padrão admin/admin, criado automaticamente — o heartbeat para o
# Central já roda embutido no "serve" (1s por padrão, use
# -heartbeat-interval para mudar); configure a URL/CA pela tela
# "Configurações" da UI web, OU rode o heartbeat como processo separado
# (-central-ca aceita um arquivo) se preferir não usar a UI:
# ./gobackup-agent heartbeat -central https://<central-host>:61443 \
#   -db "..." -token-file /etc/gobackup/agent-token \
#   -central-ca ./gobackup-central-cert.pem -interval 1s &
```

Para produção, rode `serve` como serviço systemd (`Restart=always`), não em
background manual.

## Uso

### Interface web

- Local do agente: `https://<agente>:61081/` — mesma UI do Central, sem o
  dashboard agregador.
- Central: `https://<central>:61443/` — dashboard com todos os agentes;
  clique em "administrar" para abrir a mesma tela de administração do
  agente, executada remotamente via fila de comandos.

Telas: navegador de diretórios (origem/destino), criar+rodar backup de
arquivos (completo/incremental/diferencial; destino local/NAS/SSH), criar
+rodar backup de banco (MariaDB/MySQL/PostgreSQL/SQLite; banco específico
ou todos; gzip/zip/nenhuma compressão), atividade recente, canais de
notificação (e-mail/Telegram), e (só no agente) **Configurações** — apontar/
reconfigurar para qual Central este agente reporta.

### CLI do agente (administração local, sem precisar da UI)

```bash
# backup de arquivos
gobackup-agent run -db "<conn>" -name <job> -source /dir -dest /outro/dir \
  -type full|incremental|differential [-link-dest /baseline] \
  [-dest-type local|nas|ssh] [-ssh-host H -ssh-port P -ssh-user U -ssh-key K]

# backup de banco
gobackup-agent run-db -db "<conn>" -name <job> -engine mariadb|mysql|postgresql|sqlite \
  -host H -port P -user U -password S -database <nome-ou-__ALL__> \
  -compression gzip|zip|none -dest /dir [-dest-type ...]
```

### Notificações

Cadastre um canal em `/notifications` no Central (e-mail via SMTP, ou
Telegram via bot token + chat_id). Disparos automáticos:
- `job_failed`: quando um comando remoto (backup disparado pelo Central)
  falha.
- `agent_offline`: quando um agente para de enviar heartbeat por mais de
  5s (checado a cada 3s, um alerta por queda).

## Limitações conhecidas (próximos passos)

- Multi-tenant (isolamento por organização/cliente na UI) ainda não
  implementado — o schema já tem `tenant_id` em tudo, falta a camada de
  usuários/seleção de tenant.
- Agendamento (cron) dos jobs ainda não roda automaticamente no agente —
  hoje todo disparo é manual (CLI, UI local, ou comando remoto do Central).
- `password_ref` em `db_connections` não usa um cofre de segredos real
  ainda — a senha só trafega no payload do comando (TLS), não é persistida.
- API JSON administrativa redireciona para `/login` em vez de responder
  401 quando sem sessão (adequado para o fluxo web, não para automação
  programática — se precisar de API token separado, é um próximo passo).
