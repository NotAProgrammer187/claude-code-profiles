# claude-code-profiles

Switch between multiple Claude Code accounts without logging out. A terminal
picker that gives each account its own isolated config directory.

```
  _____                                                                      _____
 ( ___ )--------------------------------------------------------------------( ___ )
  |   |                                                                      |   |
  |   |   ____ _                 _        ____             __ _ _            |   |
  |   |  / ___| | __ _ _   _  __| | ___  |  _ \ _ __ ___  / _(_) | ___  ___  |   |
  |   | | |   | |/ _` | | | |/ _` |/ _ \ | |_) | '__/ _ \| |_| | |/ _ \/ __| |   |
  |   | | |___| | (_| | |_| | (_| |  __/ |  __/| | | (_) |  _| | |  __/\__ \ |   |
  |   |  \____|_|\__,_|\__,_|\__,_|\___| |_|   |_|  \___/|_| |_|_|\___||___/ |   |
  |   |                                                by NotAProgrammer187  |   |
  |___|                                                                      |___|
 (_____)--------------------------------------------------------------------(_____)

  Switch Claude Code accounts without logging out
  v0.1.0 · 3 profiles · 2 signed in

  ─────────────────────────────────────────────────────────────────────

  ▌ work                you@example.com
    max · signed in · used 12m ago · Example Ltd

    client              you@client.dev
    max · signed in · used yesterday

    personal            —
    not signed in

  ↑↓ move   ⏎ launch   n new   i import   r rename   d delete   q quit
```

The banner steps down to a compact wordmark, then to a single line, as the
terminal gets smaller — the profile list always keeps its space.

## How it works

Claude Code keeps everything — credentials, `settings.json`, MCP servers,
`CLAUDE.md`, session history — in a single directory, and reads the
environment variable `CLAUDE_CONFIG_DIR` to decide which directory that is.

So a profile here is just a directory:

```
%USERPROFILE%\.ccswitch\
  state.json                 last-used timestamps
  profiles\
    work\                    <- a complete CLAUDE_CONFIG_DIR
      .credentials.json
      .claude.json
      settings.json
      projects\
    client\
    personal\
```

Launching a profile sets `CLAUDE_CONFIG_DIR` for that one child process and
runs `claude`. Nothing is copied, moved, or overwritten on switch, which
matters:

- **No token clobbering.** Credential-swapping tools copy files into the live
  store. If Claude Code refreshes a token mid-swap, you can corrupt a login.
  Here each account only ever writes to its own directory.
- **Accounts run in parallel.** Open three terminals, launch a different
  profile in each. They don't know about each other.
- **Switching is instant** because there's nothing to switch — only a variable
  that gets a different value.

## Install

Needs Go 1.22+. The output is one static `.exe` with no runtime dependency.

```powershell
git clone https://github.com/NotAProgrammer187/claude-code-profiles
cd claude-code-profiles
.\build.ps1 -Install
```

Then open a new terminal and run `ccswitch`.

Or, if you already have Go set up:

```bash
go install github.com/NotAProgrammer187/claude-code-profiles/cmd/ccswitch@latest
```

Building by hand, from any OS:

```bash
go mod tidy
go build -ldflags="-s -w" -o ccswitch ./cmd/ccswitch
```

## Usage

| | |
|---|---|
| `ccswitch` | open the picker |
| `ccswitch run work` | launch straight into a profile |
| `ccswitch run work -- --resume` | anything after `--` is passed to `claude` |
| `ccswitch list` | print profiles |
| `ccswitch where work` | print a profile's config directory |

**First run:** press `i` to import the account you're already logged into.
That copies your existing `~/.claude` and `~/.claude.json` into a profile, so
you keep your settings, MCP servers and session history. Then press `n` for
each additional account — a new profile starts empty, so Claude Code runs its
normal login flow the first time you launch it. Choose the "Claude account
with subscription" option at that prompt if you're using a paid plan rather
than an API key.

## Things worth knowing

- **Restart to switch.** A running Claude Code session reads its config at
  startup. Launching a different profile means a new process; it won't change
  the account under a session that's already open.
- **`ANTHROPIC_API_KEY` overrides a subscription login.** If that variable (or
  `ANTHROPIC_AUTH_TOKEN`) is set in your shell, Claude Code bills the API key
  instead of your plan. ccswitch strips both from the child process and warns
  you when it sees them.
- **macOS is different.** Claude Code stores credentials in the Keychain there
  rather than in a file, so profile isolation is less complete than on Windows
  and Linux. This is built for Windows first.
- **The metadata display is best-effort.** The email, plan and org on each row
  are read from `.claude.json` and `.credentials.json`, which are internal
  files with no stability guarantee. If their shape changes, rows show less
  detail — switching itself is unaffected, since that only depends on the
  environment variable.
- **Profile directories hold live OAuth tokens.** They're written `0600`, but
  they're still credentials: don't sync `.ccswitch` to cloud storage or commit
  it anywhere.
- Use within the terms of your Claude plan.

## License

MIT — see [LICENSE](LICENSE).

Not affiliated with, endorsed by, or sponsored by Anthropic. "Claude" and
"Claude Code" are trademarks of Anthropic, PBC, used here only to describe
what this tool works with.
