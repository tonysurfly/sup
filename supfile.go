package sup

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strings"

	"github.com/pkg/errors"

	"gopkg.in/yaml.v2"
)

// Supfile represents the Stack Up configuration YAML file.
type Supfile struct {
	Networks Networks `yaml:"networks"`
	Commands Commands `yaml:"commands"`
	Targets  Targets  `yaml:"targets"`
	Env      EnvList  `yaml:"env"`
	Version  string   `yaml:"version"`
}

// Network is group of hosts with extra custom env vars.
type Network struct {
	Env             EnvList  `yaml:"env"`
	Inventory       string   `yaml:"inventory"`
	Hosts           []*Host  `yaml:"-"`
	HostsFromConfig []string `yaml:"hosts"`
	Bastion         string   `yaml:"bastion"` // Jump host for the environment
}

func (n *Network) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type NewNetwork Network
	if err := unmarshal((*NewNetwork)(n)); err != nil {
		return err
	}
	for _, item := range n.HostsFromConfig {
		host, err := NewHost(item)
		if err != nil {
			return err
		}
		n.Hosts = append(n.Hosts, host)
	}
	return nil
}

// Host describes how to connect to a host
type Host struct {
	Address      string
	Port         string
	User         string
	IdentityFile string
	KnownAs      string // The first Host value in SSH config, if -sshconfig flag is used
	Bastion      string // ProxyJump host for the environment
}

// GetHost returns address:port. It is passed to ssh dialer function
func (h *Host) GetHost() string {
	return fmt.Sprintf("%s:%s", h.Address, h.Port)
}

// GetHostname returns hostname as it was specified in Supfile
func (h *Host) GetHostname() string {
	if h.KnownAs != "" {
		return h.KnownAs
	}
	return h.Address
}

// Returns log prefix for a host output
func (h *Host) GetPrefixText() string {
	var prefix string
	if h.KnownAs != "" {
		prefix = h.KnownAs
	} else {
		prefix = fmt.Sprintf("%s@%s:%s", h.User, h.Address, h.Port)
	}
	return fmt.Sprintf("%s | ", prefix)
}

// NewHost parses and normalizes <user>@<host:port> from a given string and
// creates Host instance.
func NewHost(hostStr string) (*Host, error) {
	host := Host{}
	// Remove extra "ssh://" schema
	if len(hostStr) > 6 && hostStr[:6] == "ssh://" {
		hostStr = hostStr[6:]
	}

	// Split by the last "@", since there may be an "@" in the username.
	if at := strings.LastIndex(hostStr, "@"); at != -1 {
		host.User = hostStr[:at]
		hostStr = hostStr[at+1:]
	}

	// Add default user, if not set
	if host.User == "" {
		u, err := user.Current()
		if err != nil {
			return nil, err
		}
		host.User = u.Username
	}

	if strings.Contains(hostStr, "/") {
		return nil, fmt.Errorf("unexpected slash in the host URL")
	}

	// Add default port, if not set
	port := "22"
	if strings.Contains(hostStr, ":") {
		var err error
		hostStr, port, err = net.SplitHostPort(hostStr)
		if err != nil {
			return nil, err
		}
	}
	host.Address = hostStr
	host.Port = port
	// Check if we can retrieve detailed information from ssh config
	conf, found := extractedHostSSHConfig[host.Address]
	if found {
		host.User = conf.User
		host.IdentityFile = ResolvePath(conf.IdentityFile)
		host.Address = conf.HostName
		host.Port = fmt.Sprintf("%d", conf.Port)
		host.KnownAs = conf.Host[0]
		host.Bastion = conf.ProxyJump
	}
	return &host, nil
}

// Networks is a list of user-defined networks
type Networks struct {
	Names []string
	nets  map[string]Network
}

func (n *Networks) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := unmarshal(&n.nets)
	if err != nil {
		return err
	}

	var items yaml.MapSlice
	err = unmarshal(&items)
	if err != nil {
		return err
	}

	n.Names = make([]string, len(items))
	for i, item := range items {
		n.Names[i] = item.Key.(string)
	}

	return nil
}

func (n *Networks) Get(name string) (Network, bool) {
	net, ok := n.nets[name]
	return net, ok
}

func (n *Networks) Set(name string, network *Network) {
	n.nets[name] = *network
	n.Names = append(n.Names, name)
}

// Command represents command(s) to be run remotely.
type Command struct {
	Name   string   `yaml:"-"`      // Command name.
	Desc   string   `yaml:"desc"`   // Command description.
	Local  bool     `yaml:"local"`  // Run command locally
	Run    string   `yaml:"run"`    // Command(s) to be run remotelly.
	Script string   `yaml:"script"` // Load command(s) from script and run it remotelly.
	Upload []Upload `yaml:"upload"` // See Upload struct.
	Stdin  bool     `yaml:"stdin"`  // Attach localhost STDOUT to remote commands' STDIN?
	Once   bool     `yaml:"once"`   // The command should be run "once" (on one host only).
	Serial int      `yaml:"serial"` // Max number of clients processing a task in parallel.

	// API backward compatibility. Will be deprecated in v1.0.
	RunOnce bool `yaml:"run_once"` // The command should be run once only.
}

// Commands is a list of user-defined commands
type Commands struct {
	Names []string
	cmds  map[string]Command
}

func (c *Commands) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := unmarshal(&c.cmds)
	if err != nil {
		return err
	}

	var items yaml.MapSlice
	err = unmarshal(&items)
	if err != nil {
		return err
	}

	c.Names = make([]string, len(items))
	for i, item := range items {
		c.Names[i] = item.Key.(string)
	}

	return nil
}

func (c *Commands) Get(name string) (Command, bool) {
	cmd, ok := c.cmds[name]
	return cmd, ok
}

// Targets is a list of user-defined targets
type Targets struct {
	Names   []string
	targets map[string][]string
}

func (t *Targets) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := unmarshal(&t.targets)
	if err != nil {
		return err
	}

	var items yaml.MapSlice
	err = unmarshal(&items)
	if err != nil {
		return err
	}

	t.Names = make([]string, len(items))
	for i, item := range items {
		t.Names[i] = item.Key.(string)
	}

	return nil
}

func (t *Targets) Get(name string) ([]string, bool) {
	cmds, ok := t.targets[name]
	return cmds, ok
}

// Upload represents file copy operation from localhost Src path to Dst
// path of every host in a given Network.
type Upload struct {
	Src string `yaml:"src"`
	Dst string `yaml:"dst"`
	Exc string `yaml:"exclude"`
}

// EnvVar represents an environment variable
type EnvVar struct {
	Key   string
	Value string
}

func (e EnvVar) String() string {
	return e.Key + `=` + e.Value
}

// AsExport returns the environment variable as a bash export statement
func (e EnvVar) AsExport() string {
	return `export ` + e.Key + `="` + e.Value + `";`
}

// EnvList is a list of environment variables that maps to a YAML map,
// but maintains order, enabling late variables to reference early variables.
type EnvList []*EnvVar

func (e EnvList) Slice() []string {
	envs := make([]string, len(e))
	for i, env := range e {
		envs[i] = env.String()
	}
	return envs
}

func (e *EnvList) UnmarshalYAML(unmarshal func(interface{}) error) error {
	items := []yaml.MapItem{}

	err := unmarshal(&items)
	if err != nil {
		return err
	}

	*e = make(EnvList, 0, len(items))

	for _, v := range items {
		e.Set(fmt.Sprintf("%v", v.Key), fmt.Sprintf("%v", v.Value))
	}

	return nil
}

// Set key to be equal value in this list.
func (e *EnvList) Set(key, value string) {
	for i, v := range *e {
		if v.Key == key {
			(*e)[i].Value = value
			return
		}
	}

	*e = append(*e, &EnvVar{
		Key:   key,
		Value: value,
	})
}

func (e *EnvList) ResolveValues() error {
	if len(*e) == 0 {
		return nil
	}

	exports := ""
	for i, v := range *e {
		exports += v.AsExport()

		cmd := exec.Command("bash", "-c", exports+"echo -n "+v.Value+";")
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cmd.Dir = cwd
		resolvedValue, err := cmd.Output()
		if err != nil {
			return errors.Wrapf(err, "resolving env var %v failed", v.Key)
		}

		(*e)[i].Value = string(resolvedValue)
	}

	return nil
}

func (e *EnvList) AsExport() string {
	// Process all ENVs into a string of form
	// `export FOO="bar"; export BAR="baz";`.
	exports := ``
	for _, v := range *e {
		exports += v.AsExport() + " "
	}
	return exports
}

type ErrMustUpdate struct {
	Msg string
}

type ErrUnsupportedSupfileVersion struct {
	Msg string
}

func (e ErrMustUpdate) Error() string {
	return fmt.Sprintf("%v\n\nPlease update sup by `go get -u github.com/pressly/sup/cmd/sup`", e.Msg)
}

func (e ErrUnsupportedSupfileVersion) Error() string {
	return fmt.Sprintf("%v\n\nCheck your Supfile version (available latest version: v0.5)", e.Msg)
}

// NewSupfile parses configuration file and returns Supfile or error.
func NewSupfile(data []byte) (*Supfile, error) {
	var conf Supfile

	if err := yaml.Unmarshal(data, &conf); err != nil {
		return nil, err
	}

	// API backward compatibility. Will be deprecated in v1.0.
	switch conf.Version {
	case "":
		conf.Version = "0.1"
		fallthrough

	case "0.1":
		for _, cmd := range conf.Commands.cmds {
			if cmd.RunOnce {
				return nil, ErrMustUpdate{"command.run_once is not supported in Supfile v" + conf.Version}
			}
		}
		fallthrough

	case "0.2":
		for _, cmd := range conf.Commands.cmds {
			if cmd.Once {
				return nil, ErrMustUpdate{"command.once is not supported in Supfile v" + conf.Version}
			}
			if cmd.Local {
				return nil, ErrMustUpdate{"command.local is not supported in Supfile v" + conf.Version}
			}
			if cmd.Serial != 0 {
				return nil, ErrMustUpdate{"command.serial is not supported in Supfile v" + conf.Version}
			}
		}
		for _, network := range conf.Networks.nets {
			if network.Inventory != "" {
				return nil, ErrMustUpdate{"network.inventory is not supported in Supfile v" + conf.Version}
			}
		}
		fallthrough

	case "0.3":
		var warning string
		for key, cmd := range conf.Commands.cmds {
			if cmd.RunOnce {
				warning = "Warning: command.run_once was deprecated by command.once in Supfile v" + conf.Version + "\n"
				cmd.Once = true
				conf.Commands.cmds[key] = cmd
			}
		}
		if warning != "" {
			fmt.Fprintln(os.Stderr, warning)
		}

		fallthrough

	case "0.4", "0.5":

	default:
		return nil, ErrUnsupportedSupfileVersion{"unsupported Supfile version " + conf.Version}
	}

	return &conf, nil
}

// ParseInventory runs the inventory command, if provided, and appends
// the command's output lines to the manually defined list of hosts.
func (n Network) ParseInventory() ([]*Host, error) {
	if n.Inventory == "" {
		return nil, nil
	}

	cmd := exec.Command("/bin/sh", "-c", n.Inventory)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, n.Env.Slice()...)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var hosts []*Host
	buf := bytes.NewBuffer(output)
	for {
		host, err := buf.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		host = strings.TrimSpace(host)
		// skip empty lines and comments
		if host == "" || host[:1] == "#" {
			continue
		}

		hosts = append(hosts, &Host{Address: host})
	}
	return hosts, nil
}
