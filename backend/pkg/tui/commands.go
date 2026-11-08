package tui

type commandID uint8

const (
	trustCommand commandID = iota
	quitCommand
	sessionsCommand
	newSessionCommand
	resumeCommand
	providersCommand
	modelsCommand
	searchSetupCommand
)

type command struct {
	id          commandID
	name        string
	aliases     []string
	description string
}

func (entry command) Title() string       { return entry.description }
func (entry command) Description() string { return entry.name }
func (entry command) FilterValue() string { return entry.name }

var commands = []command{
	{id: trustCommand, name: "/trust", description: "Toggle project skill trust"},
	{id: quitCommand, name: "/quit", aliases: []string{"/exit"}, description: "Exit Alfred"},
	{id: sessionsCommand, name: "/sessions", description: "Switch session"},
	{id: newSessionCommand, name: "/new", aliases: []string{"/clear"}, description: "New session"},
	{id: resumeCommand, name: "/resume", aliases: []string{"/continue"}, description: "Resume run"},
	{id: providersCommand, name: "/providers", description: "Providers"},
	{id: modelsCommand, name: "/models", aliases: []string{"/model"}, description: "Models"},
	{id: searchSetupCommand, name: "/web-search", description: "Web search"},
}

func findCommand(name string) (command, bool) {
	for _, candidate := range commands {
		if candidate.name == name {
			return candidate, true
		}
		for _, alias := range candidate.aliases {
			if alias == name {
				return candidate, true
			}
		}
	}
	return command{}, false
}
