package cli

import (
	"errors"
	"github.com/quanchobi/mcupdater/internal/config"
)

type command struct {
	Name string
	Args []string
}

type commands map[string]func(*config.Config, command) error

func (c *commands) run(cfg *config.Config, cmd command) error {
	f, ok := (*c)[cmd.Name]
	if !ok {
		return errors.New("command not found")
	}
	return f(cfg, cmd)
}

func (c *commands) register(name string, f func(*config.Config, command) error) {
	(*c)[name] = f
}
