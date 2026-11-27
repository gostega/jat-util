# Login shells read this file, not ~/.bashrc — and every new macOS terminal
# window is a login shell. So: put Homebrew on PATH (bash, unlike zsh, gets no
# brew shellenv from ~/.zprofile), then hand over to ~/.bashrc, where
# oh-my-bash lives.
if [ -x /opt/homebrew/bin/brew ]; then
  eval "$(/opt/homebrew/bin/brew shellenv)"
elif [ -x /usr/local/bin/brew ]; then
  eval "$(/usr/local/bin/brew shellenv)"
fi

[ -f ~/.bashrc ] && . ~/.bashrc
