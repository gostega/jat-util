# Login shells read this file, not ~/.bashrc — and every new macOS terminal
# window is a login shell. In order: Homebrew on PATH (bash, unlike zsh, gets
# no brew shellenv from ~/.zprofile), so everything after it can find brew's
# tools; then ~/.profile, for login settings shared with other shells; then
# ~/.bashrc, where oh-my-bash lives.
if [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
elif [ -x /usr/local/bin/brew ]; then
  eval "$(/usr/local/bin/brew shellenv)"
fi

[ -f ~/.profile ] && . ~/.profile
[ -f ~/.bashrc ] && . ~/.bashrc
