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
	"io/ioutil"
	"path/filepath"
	"strconv"


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
	Env                  EnvList  `yaml:"env"`
	Inventory            string   `yaml:"inventory"`
	AnsibleInventoryFile string   `yaml:"ansible_inventory_file,omitempty"` // New field
	Hosts                []*Host  `yaml:"-"`
	HostsFromConfig      []string `yaml:"hosts"`
	Bastion              string   `yaml:"bastion"` // Jump host for the environment
}

func (n *Network) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type NewNetwork Network // Use a type alias to avoid recursion
	if err := unmarshal((*NewNetwork)(n)); err != nil {
		return err
	}

	if n.AnsibleInventoryFile != "" {
		// ResolvePath is assumed to be available in the package, like in NewHost
		absPath, err := filepath.Abs(ResolvePath(n.AnsibleInventoryFile))
		if err != nil {
			return errors.Wrapf(err, "failed to get absolute path for ansible inventory file: %s", n.AnsibleInventoryFile)
		}

		ansibleHosts, err := parseAnsibleInventoryFile(absPath)
		if err != nil {
			// If Ansible inventory parsing fails, we might want to fall back or just error out.
			// For now, let's error out as per instruction "If AnsibleInventoryFile is specified and the file is successfully parsed, its hosts should be used."
			return errors.Wrapf(err, "failed to parse ansible inventory file: %s", n.AnsibleInventoryFile)
		}

		// If successful, these hosts take precedence.
		n.Hosts = ansibleHosts
		n.HostsFromConfig = nil // Clear any hosts from HostsFromConfig
		n.Inventory = ""      // Clear inventory command to prevent it from running
	} else {
		// Existing logic for HostsFromConfig if AnsibleInventoryFile is not provided
		for _, item := range n.HostsFromConfig {
			host, err := NewHost(item)
			if err != nil {
				return err
			}
			n.Hosts = append(n.Hosts, host)
		}
	}
	return nil
}

// parseAnsibleInventoryFile reads and parses an Ansible YAML inventory file.
func parseAnsibleInventoryFile(filePath string) ([]*Host, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read ansible inventory file: %s", filePath)
	}

	var inventoryData AnsibleInventoryData
	if err := yaml.Unmarshal(data, &inventoryData); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal ansible inventory YAML from file: %s", filePath)
	}

	// Using a map to ensure host uniqueness based on user@address:port or similar identifier.
	// NewHost and subsequent modifications will populate the Host struct.
	// The key for extractedHosts will be host.GetHost() after potential modifications.
	extractedHosts := make(map[string]*Host)

	for groupName, groupData := range inventoryData {
		if groupData != nil {
			extractHostsFromAnsibleGroup(groupName, groupData, extractedHosts)
		}
	}

	// Convert map to slice
	hostsList := make([]*Host, 0, len(extractedHosts))
	for _, host := range extractedHosts {
		hostsList = append(hostsList, host)
	}

	return hostsList, nil
}

// extractHostsFromAnsibleGroup recursively extracts hosts from an Ansible group
// and its children.
func extractHostsFromAnsibleGroup(groupName string, groupData *AnsibleGroup, extractedHosts map[string]*Host) {
	// Extract hosts from the current group
	if groupData.Hosts != nil {
		for hostKey, hostEntry := range groupData.Hosts {
			var address string
			var user string
			var port int

			// Determine the primary address for NewHost
			if hostEntry != nil && hostEntry.AnsibleHost != "" {
				address = hostEntry.AnsibleHost
			} else {
				address = hostKey // Use the map key (alias) as address if ansible_host is not set
			}

			// If hostEntry has user/port, prepare them
			if hostEntry != nil {
				if hostEntry.AnsibleUser != "" {
					user = hostEntry.AnsibleUser
				}
				if hostEntry.AnsiblePort > 0 {
					port = hostEntry.AnsiblePort
				}
			}

			// Create host string for NewHost, can be complex if user or port needs to be pre-set.
			// NewHost can parse "user@host:port", but it's safer to set fields after.
			// Let NewHost handle default user and port, then override.
			host, err := NewHost(address)
			if err != nil {
				// Log or handle error, e.g., by skipping this host
				fmt.Fprintf(os.Stderr, "Warning: skipping host '%s' from ansible inventory group '%s' due to error in NewHost(%s): %v\n", hostKey, groupName, address, err)
				continue
			}

			// Override user if specified in inventory
			if user != "" {
				host.User = user
			}

			// Override port if specified in inventory
			if port > 0 {
				host.Port = strconv.Itoa(port)
			}

			// If ansible_host was used as the address and it's different from the hostKey (alias), set KnownAs.
			// NewHost might also set KnownAs from SSH config. This explicit KnownAs from inventory alias should be considered.
			// If host.Address (after NewHost potentially resolves it via SSH config) is different from hostKey,
			// and host.KnownAs is not already set to hostKey by NewHost, then set it.
			if hostEntry != nil && hostEntry.AnsibleHost != "" && hostKey != hostEntry.AnsibleHost {
				host.KnownAs = hostKey
			} else if hostEntry == nil && hostKey != host.Address && host.KnownAs == "" {
				// If it's a simple host entry (e.g., "myhost:") and NewHost resolved "myhost" to a different IP,
				// set KnownAs to "myhost".
				host.KnownAs = hostKey
			}


			// Add/replace host in the map to ensure uniqueness and that variables are applied.
			// Keying by GetHost() which is address:port. If user changes, it's still the same target machine.
			// If multiple ansible entries point to the same machine but with different effective users/ports due to vars,
			// the last one processed would win if keyed simply by address:port.
			// For now, address:port is the uniqueness constraint from GetHost().
			extractedHosts[host.GetHost()] = host
		}
	}

	// Recursively process children
	if groupData.Children != nil {
		for childGroupName, childGroupData := range groupData.Children {
			if childGroupData != nil {
				extractHostsFromAnsibleGroup(childGroupName, childGroupData, extractedHosts)
			}
		}
	}
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
		// Use h.GetHostname() for prefix to prefer KnownAs if available through other means too.
		prefixHostname := h.GetHostname()
		if strings.Contains(prefixHostname, ":") { // if GetHostname somehow returns host:port
			prefixHostname = h.Address // fallback to just address if GetHostname is complex
		}
		if len(prefixHostname) > 25 { // Keep prefix from being too long
			prefixHostname = prefixHostname[:22] + "..."
		}
		prefix = fmt.Sprintf("%s@%s:%s", h.User, prefixHostname, h.Port)
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

// AnsibleHostEntry represents a host entry in the Ansible inventory.
// It can be a simple string or a map with variables.
type AnsibleHostEntry struct {
	AnsibleHost string                 `yaml:"ansible_host,omitempty"`
	AnsibleUser string                 `yaml:"ansible_user,omitempty"`
	AnsiblePort int                    `yaml:"ansible_port,omitempty"`
	Vars        map[string]interface{} `yaml:",inline"` // For other arbitrary vars
}

// AnsibleGroup represents a group in the Ansible inventory.
type AnsibleGroup struct {
	Hosts    map[string]*AnsibleHostEntry `yaml:"hosts,omitempty"`
	Vars     map[string]interface{}       `yaml:"vars,omitempty"`
	Children map[string]*AnsibleGroup     `yaml:"children,omitempty"`
}

// AnsibleInventoryData is the top-level structure for Ansible inventory.
// It's a map of group names to AnsibleGroup structs.
type AnsibleInventoryData map[string]*AnsibleGroup

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
// This should NOT be called if hosts were successfully loaded from AnsibleInventoryFile.
func (n Network) ParseInventory() ([]*Host, error) {
	if n.Inventory == "" { // Check if inventory command is cleared or not set
		return nil, nil
	}

	// If n.Hosts is already populated (e.g. by Ansible inventory), this function might
	// append to them or replace them, depending on how it's called by sup.go.
	// The modification in UnmarshalYAML to clear n.Inventory aims to prevent this.
	fmt.Fprintln(os.Stderr, "Warning: Executing command-based inventory. This should not happen if Ansible inventory was used and configured to be authoritative.")

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
