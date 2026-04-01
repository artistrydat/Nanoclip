# NanoClip

**Open-source AI agent orchestration platform.**  
Run teams of AI agents that respond to issues, chat in threads, and work autonomously — all from a **single binary** on any machine, including Android phones running Termux.

---

## What it does

NanoClip lets you create AI agents backed by local or cloud models and assign them to issues in projects. Agents:

- **Respond to user comments** on issues, maintaining full conversation history
- **Stream live output** so you can watch progress in real time
- **Record activity** — every run, message, and cost event is logged
- **Create sub-issues** when they break work down further after a run
- **Run on a schedule** via Routines (cron-style triggers)

All state is stored in a single SQLite file (default) or MariaDB (recommended for production). The whole platform ships as one Go binary that also serves the React UI.

---

## Adapter types

Only three adapter types are supported — by design, to keep the surface area small:

| Type | Description |
|------|-------------|
| `ollama_local` | Runs against a local [Ollama](https://ollama.com) instance. Point it at `http://localhost:11434` or any remote URL. |
| `openrouter_local` | Routes through [OpenRouter](https://openrouter.ai) — access 100+ models with one API key. |
| `http` | Calls any HTTP webhook endpoint. Bring your own agent runner. |

---

## Architecture

```
nanoclip/
├── go-server/          # Go backend (Gin + GORM + SQLite/MariaDB)
│   ├── handlers/       # REST API route handlers
│   ├── models/         # GORM models (agents, issues, runs, inbox, …)
│   ├── services/       # Heartbeat loop — agent run scheduler
│   ├── ws/             # WebSocket hub for live events
│   ├── middleware/      # Auth (session cookie + agent JWT)
│   └── scripts/        # Dev, build, and Termux scripts
└── ui/                 # React + Vite + Tailwind frontend
    └── src/
        ├── adapters/   # Per-adapter UI config forms
        ├── api/        # Typed API client
        ├── components/ # Shared UI components
        └── pages/      # Route pages
```

The frontend is embedded into the Go binary at build time — no separate web server needed in production.

---

## Quickstart (Desktop / Server)

### Prerequisites

- Go 1.21+
- Node.js 20+ and pnpm

### Development

```bash
# Install frontend dependencies
pnpm install

# Terminal 1: build and start the Go backend (port 8080)
bash go-server/scripts/run-dev.sh

# Terminal 2: start the Vite dev server (port 5000, proxies /api → :8080)
pnpm --filter @nanoclip/ui dev
```

Open `http://localhost:5000`.

### Production build

```bash
# 1. Build the React frontend
pnpm --filter @nanoclip/ui build

# 2. Build the Go binary (embeds ui/dist/ automatically)
cd go-server && go build -o nanoclip .

# 3. Run
./nanoclip
```

The binary listens on port `8080` by default. Set `GO_PORT` to override.

---

## Termux Setup (Android ARM64)

This section walks through running NanoClip entirely on an Android device using [Termux](https://termux.dev).

### 1. Install Termux

Download **Termux** from [F-Droid](https://f-droid.org/packages/com.termux/) (recommended — the Play Store version is outdated).

### 2. Install packages

Open Termux and run:

```bash
pkg update && pkg upgrade -y
pkg install -y golang nodejs-lts mariadb git
```

> **Note:** `nodejs-lts` provides Node.js. If it's not available, try `pkg install nodejs`.

Install pnpm:

```bash
npm install -g pnpm
```

### 3. Clone NanoClip

```bash
git clone https://github.com/artistrydat/Nanoclip.git
cd Nanoclip
```

### 4. Set up MariaDB

MariaDB is recommended on Termux because SQLite may have file-locking issues on some Android kernels.

Initialize and start MariaDB:

```bash
bash go-server/scripts/setup-mariadb.sh
```

This script will:
- Initialize the MariaDB data directory at `~/.nanoclip/mariadb/`
- Start the MariaDB daemon in the background
- Create the `nanoclip` database, user `nanoclip`, and password `nanoclip`
- Print the `MARIADB_DSN` value to paste into your `.env`

You should see output ending with:

```
Add to your .env:
  MARIADB_DSN=nanoclip:nanoclip@tcp(127.0.0.1:3306)/nanoclip?charset=utf8mb4&parseTime=True&loc=UTC
```

### 5. Create a `.env` file

In the project root, create `.env`:

```bash
cat > .env <<'EOF'
MARIADB_DSN=nanoclip:nanoclip@tcp(127.0.0.1:3306)/nanoclip?charset=utf8mb4&parseTime=True&loc=UTC
JWT_SECRET=change-me-to-a-random-string
LOCAL_TRUSTED=true
GO_PORT=8080
EOF
```

> Set `LOCAL_TRUSTED=true` to skip login entirely — recommended for personal use on your phone.  
> Replace `JWT_SECRET` with any random string (e.g., output of `openssl rand -hex 32`).

### 6. Build the UI

```bash
pnpm install
pnpm --filter @nanoclip/ui build
```

> This step can take several minutes on a phone. Run it once; the built files are embedded into the binary.

### 7. Build and run NanoClip

```bash
cd go-server
go build -o nanoclip .
./nanoclip
```

Or, to start MariaDB automatically along with the server:

```bash
bash go-server/scripts/start.sh
```

Open your phone's browser and navigate to `http://localhost:8080`.

### 8. Keep it running (optional)

To keep NanoClip alive when you exit Termux, use `nohup`:

```bash
nohup bash go-server/scripts/start.sh &> ~/nanoclip.log &
```

Or install [Termux:Boot](https://f-droid.org/packages/com.termux.boot/) (from F-Droid) and create a start script:

```bash
mkdir -p ~/.termux/boot
cat > ~/.termux/boot/nanoclip.sh <<'EOF'
#!/data/data/com.termux/files/usr/bin/bash
cd ~/Nanoclip
bash go-server/scripts/start.sh &>> ~/nanoclip.log
EOF
chmod +x ~/.termux/boot/nanoclip.sh
```

---

## MariaDB Setup (Manual)

If you prefer to configure MariaDB yourself instead of using the setup script:

### Step 1 — Install MariaDB

**Termux:**
```bash
pkg install mariadb
```

**Ubuntu/Debian:**
```bash
sudo apt install mariadb-server
sudo systemctl start mariadb
```

**macOS (Homebrew):**
```bash
brew install mariadb
brew services start mariadb
```

### Step 2 — Create database and user

Connect to MariaDB as root:

```bash
# Termux (no root password by default):
mariadb -u root

# Linux with sudo:
sudo mariadb -u root
```

Run these SQL commands:

```sql
CREATE DATABASE IF NOT EXISTS `nanoclip`
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

CREATE USER IF NOT EXISTS 'nanoclip'@'localhost' IDENTIFIED BY 'your-password';
CREATE USER IF NOT EXISTS 'nanoclip'@'127.0.0.1' IDENTIFIED BY 'your-password';

GRANT ALL PRIVILEGES ON `nanoclip`.* TO 'nanoclip'@'localhost';
GRANT ALL PRIVILEGES ON `nanoclip`.* TO 'nanoclip'@'127.0.0.1';

FLUSH PRIVILEGES;
EXIT;
```

### Step 3 — Connect NanoClip to MariaDB

Set the `MARIADB_DSN` environment variable (or add it to `.env` in the project root):

```
MARIADB_DSN=nanoclip:your-password@tcp(127.0.0.1:3306)/nanoclip?charset=utf8mb4&parseTime=True&loc=UTC
```

NanoClip uses GORM and will **auto-migrate all tables** on first start — no manual schema creation needed.

### Step 4 — Verify the connection

Start NanoClip and look for this line in the logs:

```
[db] connecting to MariaDB...
[db] migrations applied
```

If you see `[db] using SQLite`, the `MARIADB_DSN` variable was not picked up — check your `.env` file path and syntax.

---

## Configuration reference

All configuration is via environment variables (or a `.env` file in the project root):

| Variable | Default | Purpose |
|----------|---------|---------|
| `GO_PORT` | `8080` | HTTP port the server listens on |
| `MARIADB_DSN` | *(unset)* | MariaDB connection string. If unset, SQLite is used. |
| `NANOCLIP_DATA_DIR` | `~/.nanoclip/` | Directory for SQLite database file |
| `JWT_SECRET` | auto-generated | Secret for agent JWT tokens. Set explicitly in production. |
| `LOCAL_TRUSTED` | `false` | Set to `true` to skip auth entirely (single-user / local mode) |

### Local trusted mode

Set `LOCAL_TRUSTED=true` to run without authentication. A `local-system-user` account with `instance_admin` role is created automatically. Ideal for running on a personal machine or phone where you are the only user.

---

## Cross-compiling for Termux (from another machine)

If you want to build the ARM64 binary on a faster machine and copy it to your phone:

```bash
bash go-server/scripts/build-termux.sh
```

This produces `go-server/nanoclip-arm64`. Copy it to your phone:

```bash
adb push go-server/nanoclip-arm64 /sdcard/nanoclip
# Then in Termux:
cp /sdcard/nanoclip ~/nanoclip
chmod +x ~/nanoclip
```

---

## API overview

The REST API is served under `/api/`. Key route groups:

- **`/api/auth`** — sign up, sign in, sign out, get session
- **`/api/companies/:id`** — agents, issues, projects, routines, costs, members, skills, secrets, inbox, approvals, dashboard
- **`/api/agents/:id`** — agent detail, permissions, runtime state, config revisions, skills
- **`/api/issues/:id`** — comments, activity, sub-issues, runs, attachments, mark read/unread
- **`/api/heartbeat-runs/:id`** — run events, log stream, cancel
- **`/api/instance`** — instance-level settings and user management

**WebSocket:** `/api/companies/:id/events/ws` — streams live run events, agent status changes, sub-issue creation, and inbox updates.

---

## Inbox & badge

Every agent comment on an issue creates an `InboxItem`. The sidebar badge shows only **unread** items. Marking an issue read sets it to `read`; archiving sets it to `archived`. The badge clears immediately on read.

---

## Contributing

1. Fork the repo
2. Create a feature branch: `git checkout -b my-feature`
3. Make your changes and verify: `go vet ./...` and `pnpm --filter @nanoclip/ui build`
4. Open a pull request

Bug reports and feature requests welcome via [GitHub Issues](https://github.com/artistrydat/Nanoclip/issues).

---

## License

MIT — see [LICENSE](LICENSE).
