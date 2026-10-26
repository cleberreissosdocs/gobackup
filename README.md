# GoBackup

Sistema de backup para servidores Linux, com um agente local (administração
de backups de arquivos e bancos de dados) e um painel central, hospedado em
infraestrutura separada, que monitora a saúde dos backups de todos os
servidores e também pode administrá-los remotamente.

Ver `DESIGN.md` para o desenho técnico completo (modelo de dados, protocolo,
decisões de arquitetura). Este README é o guia prático de build/deploy/uso.

## Componentes

- **gobackup-agent** — instalado em cada servidor a proteger. Orquestra
  `rsync` (backup de arquivos) e `mysqldump`/`pg_dump`/`pg_dumpall`/cópia de
  arquivo (backup de banco: MariaDB, MySQL, PostgreSQL, SQLite). Tem sua
  própria interface web de administração e seu próprio Postgres local.
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

## Deploy com Docker (recomendado)

### Central

```bash
cp .env.central.example .env.central
# edite POSTGRES_PASSWORD e TLS_HOSTS (deve incluir o IP/hostname real que
# os agentes vão usar para conectar)

docker compose -f docker-compose.central.yml --env-file .env.central up -d --build

# aplicar schema e criar o primeiro usuário:
docker compose -f docker-compose.central.yml --env-file .env.central exec central \
  gobackup-central migrate -db "postgres://gobackup:<senha>@postgres:5432/gobackup?sslmode=disable"
docker compose -f docker-compose.central.yml --env-file .env.central exec central \
  gobackup-central create-user -db "postgres://gobackup:<senha>@postgres:5432/gobackup?sslmode=disable" \
  -email admin -password <escolha>

# copiar o certificado para distribuir aos agentes:
docker compose -f docker-compose.central.yml --env-file .env.central cp \
  central:/data/gobackup-central-cert.pem ./gobackup-central-cert.pem
```

Acesse `https://<host>:8443/` (aceite o certificado autoassinado no
navegador, ou instale-o como CA confiável).

### Agent (em cada servidor a proteger)

```bash
cp .env.agent.example .env.agent
# edite POSTGRES_PASSWORD, BACKUP_SOURCE_DIR (diretório do host que será
# exposto em /backups dentro do container — é o que você digita como
# origem/destino na UI), SSH_KEY_DIR se for usar destino SSH.
# Não precisa mais definir a URL do Central aqui — isso é feito depois,
# pela própria interface web.

docker compose -f docker-compose.agent.yml --env-file .env.agent up -d --build postgres

docker compose -f docker-compose.agent.yml --env-file .env.agent run --rm agent \
  migrate -db "postgres://gobackup:***@postgres:5432/gobackup?sslmode=disable"
docker compose -f docker-compose.agent.yml --env-file .env.agent run --rm agent \
  create-user -db "postgres://gobackup:***@postgres:5432/gobackup?sslmode=disable" \
  -email admin -password <escolha>

docker compose -f docker-compose.agent.yml --env-file .env.agent up -d
```

Isso sobe dois containers: `postgres` (Postgres local do agente) e `agent`
(UI web em `https://<host>:8081/`, com o heartbeat para o Central rodando
embutido no mesmo processo).

Depois, abra `https://<host>:8081/`, faça login, e vá em **Configurações**
(link no topo). Lá você:
- Dá um nome para este agente (como vai aparecer no painel do Central).
- Informa a URL do Central (ex: `https://192.168.122.151:8443`).
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
./gobackup-central migrate -db "postgres://gobackup:senha@127.0.0.1:5432/gobackup?sslmode=disable"
./gobackup-central create-user -db "..." -email admin -password <escolha>
./gobackup-central serve -db "..." -addr :8443 -tls-hosts "<ip-do-central>,localhost,127.0.0.1"
# copie o gobackup-central-cert.pem gerado para cada agente

# em cada servidor protegido
sudo apt-get install -y postgresql rsync
sudo -u postgres psql -c "CREATE ROLE gobackup LOGIN PASSWORD 'senha';"
sudo -u postgres psql -c "CREATE DATABASE gobackup OWNER gobackup;"
./gobackup-agent migrate -db "postgres://gobackup:senha@127.0.0.1:5432/gobackup?sslmode=disable"
./gobackup-agent create-user -db "..." -email admin -password <escolha>
./gobackup-agent register -central https://<central-host>:8443 -name <nome> \
  -token-file /etc/gobackup/agent-token -central-ca ./gobackup-central-cert.pem
./gobackup-agent serve -db "..." -addr :8081 &
# o heartbeat para o Central já roda embutido no "serve" (1s por padrão,
# use -heartbeat-interval para mudar); configure a URL/CA pela tela
# "Configurações" da UI web, OU rode o heartbeat como processo separado
# (-central-ca aceita um arquivo) se preferir não usar a UI:
# ./gobackup-agent heartbeat -central https://<central-host>:8443 \
#   -db "..." -token-file /etc/gobackup/agent-token \
#   -central-ca ./gobackup-central-cert.pem -interval 1s &
```

Para produção, rode `serve` como serviço systemd (`Restart=always`), não em
background manual.

## Uso

### Interface web

- Local do agente: `https://<agente>:8081/` — mesma UI do Central, sem o
  dashboard agregador.
- Central: `https://<central>:8443/` — dashboard com todos os agentes;
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
