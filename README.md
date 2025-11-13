# Try - Go Port

This is a Go port of [Tobi's `try`](https://github.com/tobi/try) script - a tool for managing experiment directories with fuzzy search and smart organization.

_My motivation for porting this originally came from the assumption that I would want to add a bunch of features that would be
better suited to Go than Ruby, and also to work on some more Go stuff. But so far, the original features have been more than sufficient._

## Features

- **Interactive TUI** with fuzzy search and real-time filtering
- **Smart scoring** algorithm that considers:
  - Fuzzy match quality
  - Recently used directories (time-based scoring)
  - Directory name length
  - Date-prefixed directories get bonus points
- **Git integration**:
  - Clone repositories into date-prefixed directories
  - Create git worktrees from existing repositories
- **Shell integration** for Bash, Zsh, and Fish
- **Zero dependencies** (except for terminal handling library)

## How It Works

**Important**: This tool prints shell commands to stdout, which are then executed by a shell wrapper function. It does NOT directly create directories or execute git commands itself.

The workflow:
1. You type `try redis` in your shell
2. The shell wrapper calls `./try cd redis`
3. The Go program prints: `mkdir -p ~/src/tries/2025-01-15-redis && touch ... && cd ...`
4. The shell wrapper executes these commands

## Installation

### Prerequisites

- Go 1.21 or later
- Unix-like system (macOS, Linux) with terminal support

### Build from source

```bash
git clone https://github.com/spmurrayzzz/try
cd try
go build -o bin/try try.go
```

### Install shell function

Add to your `~/.zshrc` or `~/.bashrc`:

```bash
eval "$(./try init ~/src/tries)"
```

For Fish shell, add to `~/.config/fish/config.fish`:

```fish
eval (./try init ~/src/tries | string collect)
```

## Usage

### Commands

```bash
try                    # Browse all experiments
try redis             # Search for redis experiments
try clone <uri>        # Clone git repository
try worktree dir       # Create worktree from current directory
try init [path]        # Generate shell function
```

### Keyboard Shortcuts

- `↑/↓` or `Ctrl-P/N/J/K` - Navigate
- `Enter` - Select or create directory
- `Backspace` - Delete character from search
- `Ctrl-D` - Delete directory (with confirmation)
- `ESC` - Cancel
- Type to filter/search

### Environment Variables

- `TRY_PATH` - Override default directory (default: `~/src/tries`)

## Common Usage Patterns

### Creating a new experiment
```bash
try                    # Type to search, select "Create new"
try my-experiment     # Creates ~/src/tries/2025-01-15-my-experiment
try . my-name         # From current git repo, creates worktree
```

### Working with git repositories
```bash
# Clone a repo into dated directory
try clone https://github.com/user/repo.git

# Create worktree from current repo
try worktree           # Auto-named from current dir
try worktree . my-name  # Custom name

# Create worktree from another repo
try worktree /path/to/repo custom-name
```

## Troubleshooting

### "Command not found" errors
Make sure you've added the shell function to your profile and reloaded it:
```bash
source ~/.zshrc  # or ~/.bashrc
```

### Git worktree not created
Check that:
1. You're in a git repository (`git status` works)
2. You're using correct syntax: `try worktree . name` not `try worktree name`
3. The directory doesn't already exist

### Interactive mode not working
Ensure you're running in a terminal with TTY support. The tool requires an interactive terminal.

## Differences from Ruby Version

1. **Static typing**: Uses Go's type system with `map[string]interface{}` for flexibility
2. **Error handling**: More explicit error handling with proper error checks
3. **Terminal handling**: Uses `golang.org/x/term` instead of Ruby's `io/console`
4. **Performance**: Compiled binary is faster and has fewer runtime dependencies
5. **Symbols**: Ruby symbols (`:cd`, `:mkdir`) are replaced with strings (`"cd"`, `"mkdir"`)

## Testing

### Run basic functionality test:
```bash
go run try.go test_try.go
```

### Run Go tests:
```bash
go test -v
```

### Manual testing:
```bash
# Set up test environment
export TRY_PATH=/tmp/test-tries
mkdir -p /tmp/test-tries

# Create test directories
mkdir -p /tmp/test-tries/2025-01-15-redis-test
mkdir -p /tmp/test-tries/2025-01-14-postgres-test

# Test interactive selector
./try cd --path /tmp/test-tries

# Test from git repo
./bin/try worktree . test-from-repo
```

## Implementation Details

### UI Module

The UI module implements double-buffered terminal output with:
- Token-based styling system (`{highlight}`, `{dim_text}`, etc.)
- ANSI escape sequence handling
- TTY detection for graceful fallback
- Efficient screen updates (only redraws changed lines)

### Scoring Algorithm

The scoring algorithm considers:
1. **Match quality**: Character-by-character matching with proximity bonuses
2. **Time decay**: Recently modified directories get higher scores
3. **Name length**: Shorter names score higher for equivalent matches
4. **Date prefix**: Directories with `YYYY-MM-DD-` prefix get bonus points

### Git Integration

Supports multiple Git URI formats:
- `https://github.com/user/repo.git`
- `git@github.com:user/repo`
- `https://gitlab.com/user/repo`
- `git@host.com:user/repo`

Automatically generates directory names like `2025-01-15-user-repo`.

## License

MIT License
