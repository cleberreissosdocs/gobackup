# GoBackup — Documento de Design (v1)

Status: aprovado para início de implementação (MVP sem multi-tenant primeiro)
Data: outubro/2026

## 1. Visão geral

GoBackup é um sistema de backup para servidores Linux, com dois componentes. Cobre dois
tipos de job: **backup de arquivos** (via rsync) e **backup de banco de dados** (dump +
compressão dos engines MariaDB, MySQL, PostgreSQL e SQLite).

1. **GoBackup Agent** — instalado em cada servidor a ser protegido. Orquestra o `rsync`,
   tem sua própria interface web de administração de backups (igual em funcionalidade ao
   Kopia), e roda de forma autônoma (continua fazendo backup mesmo se o Central estiver fora do ar).
2. **GoBackup Central** — aplicação web hospedada em infraestrutura separada (propositalmente
   longe/isolada dos servidores protegidos), multi-tenant, que:
   - mostra um dashboard de saúde de backup de todos os servidores/clientes cadastrados;
   - tem as MESMAS opções de administração de backup do Agent (pode criar/editar/iniciar
     jobs remotamente, navegar diretórios de destino);
   - NÃO armazena os dados de backup em si, apenas metadados/status.

Diferencial em relação a produtos como Kopia: a existência do Central como ponto único e
isolado de observabilidade de todos os backups, pensado para sobreviver mesmo que um dos
servidores protegidos (ou sua infra) caia por completo.

## 2. Componentes e repositório

Monorepo Go com dois binários e um core compartilhado:

```
gobackup/
  cmd/
    agent/        main.go do agente
    central/      main.go do central
  internal/
    core/         entidades e regras de domínio compartilhadas
    backup/       orquestração de rsync (full / incremental / differential)
    storage/      acesso a destinos: local, NAS, SSH
    api/          handlers HTTP (REST), comuns entre agent e central
    web/          UI (Go templates + htmx)
    db/           migrations + queries (sqlc)
    poller/       cliente de polling (lado agente)
    queue/        fila de comandos (lado central)
    notify/       email + telegram
    auth/         login/sessão simples
  migrations/
  web/static/
```

## 3. Modelo de dados (PostgreSQL)

```sql
-- multi-tenant (schema já previsto desde o início, mesmo que o MVP use 1 tenant fixo)
tenants (id, name, created_at)

users (id, tenant_id, email, password_hash, role[admin|operator|viewer], created_at)

-- cada servidor protegido = um agente cadastrado no central
agents (
  id, tenant_id, name, hostname,
  api_token_hash,
  status[online|offline|unknown],
  last_seen_at, agent_version, created_at
)

-- destinos de backup: local, NAS ou SSH
destinations (
  id, agent_id, type[local|nas|ssh],
  path,
  nas_config jsonb,   -- host/protocolo (NFS/SMB), share, credenciais (referência, não em texto puro)
  ssh_config jsonb,   -- host, port, user, key_ref
  created_at
)

-- conexões de banco de dados cadastradas no agente (fonte de backups do tipo "database")
db_connections (
  id, agent_id, name,
  engine[mariadb|mysql|postgresql|sqlite],
  host, port,                 -- vazio/ignorado para sqlite
  username, password_ref,     -- password_ref = referência a secret, nunca texto puro na tabela
  database_name,               -- "__ALL__" (todos os bancos) ou nome específico do banco/schema
  sqlite_path,                 -- caminho do arquivo .db/.sqlite, só para engine=sqlite
  created_at
)

backup_jobs (
  id, tenant_id, agent_id, name,
  job_kind[files|database],

  -- usados quando job_kind=files
  source_path, destination_id,
  backup_type[full|incremental|differential],

  -- usados quando job_kind=database
  db_connection_id,            -- fk -> db_connections
  dump_destination_id,         -- fk -> destinations (diretório configurado na UI onde salvar o dump)
  compression[gzip|zip|none],

  schedule_cron, retention_policy jsonb,
  enabled, created_at, updated_at
)

job_executions (
  id, job_id,
  status[queued|running|success|failed|cancelled],
  trigger[scheduled|manual|remote_central],
  started_at, finished_at,
  bytes_transferred, files_transferred,
  error_message, log_ref
)

-- fila de comandos do Central para o Agent, consumida via polling
agent_commands (
  id, agent_id,
  type[create_job|update_job|delete_job|start_job|cancel_job|list_dir],
  payload jsonb,
  status[pending|delivered|acked|failed],
  created_at, delivered_at, acked_at,
  result jsonb
)

notification_channels (
  id, tenant_id, type[email|telegram],
  config jsonb,   -- endereço, chat_id, etc.
  events jsonb    -- quais eventos disparam (job_failed, job_delayed, ...)
)
```

## 4. Protocolo Agente ↔ Central

O agente SEMPRE inicia a conexão (funciona atrás de NAT/firewall sem precisar abrir porta).
A cada **15 segundos** (configurável por agente) o agente faz polling:

```
POST https://central/api/v1/agents/poll
Authorization: Bearer <api_token>

Body:
{
  "status": "idle" | "running",
  "running_executions": [ ... progresso de execuções em andamento ... ],
  "last_executions_summary": [ ... execuções concluídas desde o último poll ... ],
  "command_results": [
    { "command_id": "...", "status": "acked", "result": { ... } }
  ]
}

Resposta:
{
  "commands": [
    { "id": "...", "type": "start_job", "payload": { "job_id": "..." } },
    { "id": "...", "type": "list_dir", "payload": { "path": "/mnt/backup" } }
  ]
}
```

Esse ciclo único cobre: heartbeat, progresso em tempo quase real, resultado de execuções,
entrega de comandos remotos e confirmação/resposta desses comandos.

## 5. Decisões de design fechadas

- **Agendamento (cron) roda localmente no Agent** — não depende do Central estar no ar.
  O Central apenas reflete o estado e pode disparar `start_job` sob demanda.
- **Polling, não WebSocket** — mais simples de implementar; latência de até ~15s é aceitável.
  Pode evoluir para push/WebSocket depois sem mudar o modelo de dados.
- **UI**: Go server-rendered + htmx (sem build de SPA separado). Pode evoluir visualmente depois.
- **Destinos suportados**: local (dois diretórios no mesmo servidor, caso principal), NAS, SSH remoto.
- **Tipos de backup**: completo, incremental, diferencial.
- **Sem criptografia em repouso** (ssh cobre o trânsito quando aplicável).
- **Autenticação simples** (login/senha, bcrypt) tanto no Agent quanto no Central.
- **Central não guarda dados de backup**, apenas metadados e status.
- **Paridade de funcionalidades**: Agent e Central compartilham as mesmas telas de
  administração de backup; a única exclusividade do Central é o dashboard agregador
  multi-servidor/multi-tenant.

## 6. Fluxos principais

### 6.1 Cadastro de um novo servidor
1. Admin cria o registro do agente no Central (gera `api_token`).
2. Instala o binário `gobackup-agent`, configurando `central_url` + token.
3. Agente começa o polling; Central marca `status=online`.

### 6.2 Criar job de backup pelo Central (remoto)
1. Usuário escolhe o agente no painel, clica "novo job".
2. Central enfileira `list_dir` para o agente mostrar a árvore de diretórios do destino
   (resposta chega no próximo poll — UI mostra loading).
3. Usuário define origem/destino/tipo/agendamento; Central grava `backup_jobs` e enfileira
   `create_job` para o agente aplicar seu cron local.

### 6.3 Iniciar backup manualmente pelo Central
1. Central enfileira `start_job`.
2. Agente executa `rsync`, reporta progresso nos polls seguintes.
3. Central atualiza `job_executions` e o dashboard.

### 6.5 Execução de backup de banco de dados (job_kind=database)
1. Agendamento (local, igual aos jobs de arquivo) ou comando remoto `start_job` dispara a execução.
2. Agente lê `db_connections` do job, conecta no banco conforme o `engine`. Na UI, ao criar
   a `db_connections`/job, o usuário escolhe explicitamente **"todos os bancos"** ou **"banco
   específico"** (seleciona o nome na lista obtida do próprio servidor):
   - MySQL/MariaDB: banco específico -> `mysqldump <db>`; todos -> `mysqldump --all-databases`
     (ou `mariadb-dump`)
   - PostgreSQL: banco específico -> `pg_dump <db>`; todos -> `pg_dumpall`
   - SQLite: sempre um arquivo específico (não existe conceito de "todos" aqui) — cópia do
     arquivo (opcionalmente via `.backup` do próprio sqlite3 pra garantir consistência sem lock)
3. Agente compacta o dump (gzip ou zip, conforme `compression`).
4. Agente salva o arquivo compactado no diretório indicado em `dump_destination_id`
   (diretório configurado pelo usuário na interface web — local, NAS ou SSH, mesmas opções
   de destino dos backups de arquivo).
5. Agente registra a execução em `job_executions` (mesmo fluxo de sucesso/falha/tamanho que
   os jobs de arquivo) e reporta no próximo poll.
6. Retenção (`retention_policy`) se aplica igual aos backups de arquivo (expira dumps antigos).

### 6.6 Notificação de falha/atraso
Ao gravar `job_executions` com `status=failed`, ou detectar atraso (comparando a próxima
execução esperada com o horário atual), o Central dispara `notify/` → email e/ou Telegram,
conforme `notification_channels` do tenant.

## 7. Stack técnica

- Go 1.22+
- Router: chi ou echo
- UI: Go templates + htmx (sem SPA)
- DB: PostgreSQL + sqlc
- Migrations: goose ou golang-migrate
- Auth: sessão com cookie + bcrypt
- rsync via `os/exec`, parse de `--info=progress2` para progresso
- Backup de banco via `os/exec`: `mysqldump`/`mariadb-dump` (MySQL/MariaDB), `pg_dump`
  (PostgreSQL), cópia/`.backup` do arquivo (SQLite); compactação com `compress/gzip` ou
  `archive/zip` da stdlib (evita depender de binário externo de compressão)
- Telegram: chamada HTTP direta à Bot API
- Email: SMTP (net/smtp ou gomail)
- Deploy: systemd service para cada binário; Central também pode rodar em Docker

## 8. Plano de implementação do MVP (sem multi-tenant)

1. `gobackup-core`: entidades + migrations Postgres (schema completo já com `tenant_id`,
   mesmo usando 1 tenant fixo no MVP).
2. `gobackup-agent`: executa rsync localmente (origem/destino no mesmo servidor), job
   manual via UI local, grava execução no Postgres local.
3. Protocolo de polling: agente envia heartbeat ao central (ainda sem comandos).
4. `gobackup-central`: recebe poll, grava `agents`/`job_executions`, dashboard básico
   (lista de servidores + status).
5. Fila de comandos: central envia `start_job` / `create_job` / `list_dir`; agente executa
   e responde.
6. Suporte a destinos NAS e SSH (além do local).
7. Backup de banco de dados: `db_connections` + dump/compressão para MariaDB, MySQL,
   PostgreSQL e SQLite, salvando no destino configurado na UI.
8. Notificações (email/Telegram).
9. Multi-tenant (usuários, isolamento por `tenant_id` na UI).

## 9. Itens em aberto para quando a implementação chegar lá

- Formato exato de `nas_config` / `ssh_config` (quais protocolos de NAS suportar: NFS, SMB,
  ambos?).
- Política de retenção padrão (quantos backups completos/incrementais manter antes de expirar).
- Rotação/renovação do `api_token` dos agentes.
- Rate limit / backoff do polling quando o Central está fora do ar.
- Onde/como armazenar credenciais de `db_connections` (password_ref) de forma segura — ex:
  arquivo de secrets local criptografado no agente, ou variável de ambiente referenciada,
  nunca a senha em texto puro na tabela do Postgres.
- Para PostgreSQL, "todos os bancos" usa `pg_dumpall`; banco específico usa `pg_dump <db>`
  — decidido: a UI oferece as duas opções (já fechado).
- Para MySQL/MariaDB, "todos os bancos" usa `--all-databases`; banco específico passa o nome
  do banco — decidido: a UI oferece as duas opções (já fechado). Ainda falta decidir se bancos
  de sistema (`mysql`, `information_schema`, `performance_schema`) entram ou são excluídos por
  padrão no modo "todos".
