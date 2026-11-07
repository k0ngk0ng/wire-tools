package command

import (
	_ "embed"
	"errors"
	"fmt"
)

//go:embed completions/wirectl-talk.bash
var bashCompletionScript string

//go:embed completions/wirectl-talk.zsh
var zshCompletionScript string

func completionCommand(args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Usage: wirectl talk completion bash|zsh")
		fmt.Println("Print a shell completion script. Load it with: source <(wirectl talk completion bash)")
		return nil
	}
	if len(args) != 1 {
		return errors.New("usage: completion bash|zsh")
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletionScript)
	case "zsh":
		fmt.Print(zshCompletionScript)
	default:
		return errors.New("unsupported shell; choose bash or zsh")
	}
	return nil
}
