package sup

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"os/user"


	"gopkg.in/yaml.v2"
)

// createTempInventoryFile creates a temporary YAML file with the given content.
// It returns the path to the file and a cleanup function to be deferred.
func createTempInventoryFile(t *testing.T, content string) (filePath string, cleanup func()) {
	t.Helper()
	tmpFile, err := ioutil.TempFile("", "ansible-inventory-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if _, err := tmpFile.Write([]byte(content)); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to close temp file: %v", err)
	}
	return tmpFile.Name(), func() { os.Remove(tmpFile.Name()) }
}

// findHost searches for a host in the slice by its Address or KnownAs fields.
// Prefers matching Address if both could match.
func findHost(hosts []*Host, identifier string) *Host {
	for _, h := range hosts {
		if h.Address == identifier {
			return h
		}
	}
	// Fallback to checking KnownAs if not found by Address
	for _, h := range hosts {
		if h.KnownAs == identifier {
			return h
		}
	}
	return nil
}


// TestParseBasicAnsibleInventory tests parsing a typical Ansible inventory structure.
func TestParseBasicAnsibleInventory(t *testing.T) {
	inventoryContent := `
all:
  children:
    prod:
      hosts:
        prod-primary-eude:
        prod-backup-eude:
        prod-failover:
    socks:
      hosts:
        prod-socks-eunl-1:
  hosts:
    ungrouped-host:
`
	filePath, cleanup := createTempInventoryFile(t, inventoryContent)
	defer cleanup()

	net := &Network{
		AnsibleInventoryFile: filePath,
	}

	// Simulate the YAML unmarshalling process for the Network struct
	// We need to marshal an object that would contain the Network struct,
	// then unmarshal it to trigger Network.UnmarshalYAML.
	// Or, more directly, call a method that encapsulates the logic if UnmarshalYAML wasn't so coupled.
	// Given Network.UnmarshalYAML's signature, we'll need to simulate its call.
	// A simpler way for this specific test is to call parseAnsibleInventoryFile directly.
	// However, the task asks to test the functionality *in* supfile.go, which implies testing through Network.UnmarshalYAML.

	// Let's construct a Supfile structure to unmarshal
	supfileContent := fmt.Sprintf(`
networks:
  testnet:
    ansible_inventory_file: %s
`, filePath)

	var supf Supfile
	err := yaml.Unmarshal([]byte(supfileContent), &supf)
	if err != nil {
		t.Fatalf("Failed to unmarshal Supfile: %v", err)
	}

	parsedNet, ok := supf.Networks.Get("testnet")
	if !ok {
		t.Fatalf("Test network 'testnet' not found after unmarshal")
	}

	if len(parsedNet.Hosts) != 5 {
		t.Errorf("Expected 5 hosts, got %d", len(parsedNet.Hosts))
		for i, h := range parsedNet.Hosts {
			t.Logf("Host %d: Addr=%s, KnownAs=%s, User=%s, Port=%s", i, h.Address, h.KnownAs, h.User, h.Port)
		}
	}

	expectedHosts := []string{"prod-primary-eude", "prod-backup-eude", "prod-failover", "prod-socks-eunl-1", "ungrouped-host"}
	for _, expected := range expectedHosts {
		if host := findHost(parsedNet.Hosts, expected); host == nil {
			t.Errorf("Expected host '%s' not found", expected)
		}
	}
}

// TestParseAnsibleHostVariables tests parsing of ansible_host, ansible_user, and ansible_port.
func TestParseAnsibleHostVariables(t *testing.T) {
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("Failed to get current user: %v", err)
	}
	defaultUser := currentUser.Username

	inventoryContent := `
all:
  hosts:
    host1: # Simple host
    host2:
      ansible_host: 192.168.1.10
    host3:
      ansible_user: testuser
    host4:
      ansible_port: 2222
    host5:
      ansible_host: 192.168.1.20
      ansible_user: anotheruser
      ansible_port: 22022
    host6:
      ansible_host: host6.example.com # Test KnownAs when ansible_host is a name
`
	filePath, cleanup := createTempInventoryFile(t, inventoryContent)
	defer cleanup()

	supfileContent := fmt.Sprintf(`
networks:
  testnet:
    ansible_inventory_file: %s
`, filePath)
	var supf Supfile
	if err := yaml.Unmarshal([]byte(supfileContent), &supf); err != nil {
		t.Fatalf("Failed to unmarshal Supfile: %v", err)
	}
	parsedNet, _ := supf.Networks.Get("testnet")

	if len(parsedNet.Hosts) != 6 {
		t.Errorf("Expected 6 hosts, got %d", len(parsedNet.Hosts))
	}

	// Host1: Simple
	h1 := findHost(parsedNet.Hosts, "host1")
	if h1 == nil { t.Fatal("host1 not found"); return } // Use return to avoid nil pointer dereference
	if h1.Address != "host1" { t.Errorf("host1: Expected Address 'host1', got '%s'", h1.Address) }
	if h1.User != defaultUser {	t.Errorf("host1: Expected User '%s', got '%s'", defaultUser, h1.User) }
	if h1.Port != "22" { t.Errorf("host1: Expected Port '22', got '%s'", h1.Port) }
	if h1.KnownAs != "" && h1.KnownAs != "host1" {t.Errorf("host1: Expected KnownAs '' or 'host1', got '%s'", h1.KnownAs)}


	// Host2: ansible_host
	h2 := findHost(parsedNet.Hosts, "host2") // findHost will find by KnownAs if Address is different
	if h2 == nil { t.Fatal("host2 not found by KnownAs 'host2'"); return }
	if h2.Address != "192.168.1.10" { t.Errorf("host2: Expected Address '192.168.1.10', got '%s'", h2.Address) }
	if h2.KnownAs != "host2" { t.Errorf("host2: Expected KnownAs 'host2', got '%s'", h2.KnownAs) }
	if h2.User != defaultUser {	t.Errorf("host2: Expected User '%s', got '%s'", defaultUser, h2.User) }


	// Host3: ansible_user
	h3 := findHost(parsedNet.Hosts, "host3")
	if h3 == nil { t.Fatal("host3 not found"); return }
	if h3.User != "testuser" { t.Errorf("host3: Expected User 'testuser', got '%s'", h3.User) }
	if h3.Address != "host3" { t.Errorf("host3: Expected Address 'host3', got '%s'", h3.Address)}


	// Host4: ansible_port
	h4 := findHost(parsedNet.Hosts, "host4")
	if h4 == nil { t.Fatal("host4 not found"); return }
	if h4.Port != "2222" { t.Errorf("host4: Expected Port '2222', got '%s'", h4.Port) }
	if h4.Address != "host4" { t.Errorf("host4: Expected Address 'host4', got '%s'", h4.Address)}

	// Host5: all three
	h5 := findHost(parsedNet.Hosts, "host5") // find by KnownAs
	if h5 == nil { t.Fatal("host5 not found by KnownAs 'host5'"); return }
	if h5.Address != "192.168.1.20" { t.Errorf("host5: Expected Address '192.168.1.20', got '%s'", h5.Address) }
	if h5.User != "anotheruser" { t.Errorf("host5: Expected User 'anotheruser', got '%s'", h5.User) }
	if h5.Port != "22022" { t.Errorf("host5: Expected Port '22022', got '%s'", h5.Port) }
	if h5.KnownAs != "host5" { t.Errorf("host5: Expected KnownAs 'host5', got '%s'", h5.KnownAs) }

	// Host6: ansible_host is a name
	h6 := findHost(parsedNet.Hosts, "host6") // find by KnownAs
	if h6 == nil { t.Fatal("host6 not found by KnownAs 'host6'"); return }
	if h6.Address != "host6.example.com" { t.Errorf("host6: Expected Address 'host6.example.com', got '%s'", h6.Address) }
	if h6.KnownAs != "host6" { t.Errorf("host6: Expected KnownAs 'host6', got '%s'", h6.KnownAs) }
}

// TestParseAnsibleNestedGroups tests parsing of nested group structures.
func TestParseAnsibleNestedGroups(t *testing.T) {
	inventoryContent := `
all:
  children:
    parent_group:
      hosts:
        host_in_parent:
      children:
        child_group:
          hosts:
            host_in_child:
    another_parent:
      hosts:
        another_host:
`
	filePath, cleanup := createTempInventoryFile(t, inventoryContent)
	defer cleanup()

	supfileContent := fmt.Sprintf(`networks: { testnet: { ansible_inventory_file: "%s" } }`, filePath)
	var supf Supfile
	if err := yaml.Unmarshal([]byte(supfileContent), &supf); err != nil {
		t.Fatalf("Failed to unmarshal Supfile: %v", err)
	}
	parsedNet, _ := supf.Networks.Get("testnet")

	if len(parsedNet.Hosts) != 3 {
		t.Errorf("Expected 3 hosts, got %d", len(parsedNet.Hosts))
	}
	expectedHosts := []string{"host_in_parent", "host_in_child", "another_host"}
	for _, expected := range expectedHosts {
		if host := findHost(parsedNet.Hosts, expected); host == nil {
			t.Errorf("Expected host '%s' not found", expected)
		}
	}
}

// TestParseAnsibleHostUniqueness tests that hosts appearing in multiple groups are listed once.
func TestParseAnsibleHostUniqueness(t *testing.T) {
	inventoryContent := `
group1:
  hosts:
    shared_host:
      ansible_port: 1111
    host_g1:
group2:
  hosts:
    shared_host: # With current logic, if address is the same, this will be an update.
      ansible_port: 2222 # The port from the "last" processed entry for "shared_host" will win.
    host_g2:
`
	// The "winner" between shared_host definitions depends on map iteration order, which isn't guaranteed.
	// To make it deterministic for the test, ensure one definition is clearly distinct or test for one of the possibilities.
	// Let's assume the port 2222 wins if group2 is processed after group1 for "shared_host"
	// For the purpose of this test, we only care that it's listed once.
	// The map key in extractHostsFromAnsibleGroup is host.GetHost() which is address:port.
	// So, if ansible_port changes, they become different keys if ansible_host is not set.
	// If ansible_host is set, that's the address. If not, hostKey is address.
	// shared_host (addr) : 1111 (port) -> "shared_host:1111"
	// shared_host (addr) : 2222 (port) -> "shared_host:2222"
	// These would be two distinct entries in the map. This test needs refinement based on actual uniqueness logic.

	// Revised test for uniqueness: map key is host.GetHost() -> address:port.
	// If "shared_host" has ansible_host not set, its address is "shared_host".
	// entry 1: address="shared_host", port="1111" -> key "shared_host:1111"
	// entry 2: address="shared_host", port="2222" -> key "shared_host:2222"
	// These are treated as two different hosts by the current logic.

	// To test true uniqueness (same host, different groups, one definition wins or is primary):
	// The current implementation will create two distinct Host objects if their effective `address:port` differs.
	// If `ansible_host` is identical, and `ansible_port` is identical (or not set, defaulting to 22), then it's one entry.

	inventoryContentSameEffectiveHost := `
group1:
  hosts:
    identical_host: # Default port 22
    host_g1:
group2:
  hosts:
    identical_host: # Default port 22, same effective host
    host_g2:
`
	filePath, cleanup := createTempInventoryFile(t, inventoryContentSameEffectiveHost)
	defer cleanup()

	supfileContent := fmt.Sprintf(`networks: { testnet: { ansible_inventory_file: "%s" } }`, filePath)
	var supf Supfile
	if err := yaml.Unmarshal([]byte(supfileContent), &supf); err != nil {
		t.Fatalf("Failed to unmarshal Supfile: %v", err)
	}
	parsedNet, _ := supf.Networks.Get("testnet")

	if len(parsedNet.Hosts) != 3 { // identical_host, host_g1, host_g2
		t.Errorf("Expected 3 hosts for identical_host test, got %d", len(parsedNet.Hosts))
		for _, h := range parsedNet.Hosts { t.Logf("Host: %s:%s (KnownAs: %s)", h.Address, h.Port, h.KnownAs) }
	}
	if findHost(parsedNet.Hosts, "identical_host") == nil {
		t.Errorf("Expected 'identical_host' not found")
	}
	// Count occurrences of identical_host
	count := 0
	for _, h := range parsedNet.Hosts {
		if h.Address == "identical_host" || h.KnownAs == "identical_host" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Expected 'identical_host' to appear once, got %d times", count)
	}
}


// TestParseAnsibleFileNotFound tests behavior when the inventory file does not exist.
func TestParseAnsibleFileNotFound(t *testing.T) {
	supfileContent := `
networks:
  testnet:
    ansible_inventory_file: "/path/to/nonexistent/inventory.yaml"
`
	var supf Supfile
	err := yaml.Unmarshal([]byte(supfileContent), &supf)
	// The error should come from Network.UnmarshalYAML, which is called by Supfile's UnmarshalYAML.
	// The error from parseAnsibleInventoryFile (file reading) should be wrapped.
	if err == nil {
		t.Fatalf("Expected an error when inventory file is not found, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read ansible inventory file") && !strings.Contains(err.Error(), "failed to get absolute path") {
		// The abs path can also fail if ResolvePath returns something invalid for a non-existent file.
		t.Errorf("Expected error to contain 'failed to read ansible inventory file' or 'failed to get absolute path', got: %v", err)
	}
}

// TestParseAnsibleEmptyInventoryFile tests parsing an empty or minimal Ansible inventory.
func TestParseAnsibleEmptyInventoryFile(t *testing.T) {
	testCases := []struct {
		name           string
		content        string
		expectedHosts  int
	}{
		{"CompletelyEmpty", "", 0}, // This will likely be a YAML unmarshal error.
		{"EmptyYAML", "{}", 0},
		{"AllEmpty", "all: {}", 0},
		{"AllHostsEmpty", "all:\n  hosts: {}", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filePath, cleanup := createTempInventoryFile(t, tc.content)
			defer cleanup()

			supfileContent := fmt.Sprintf(`networks: { testnet: { ansible_inventory_file: "%s" } }`, filePath)
			var supf Supfile
			err := yaml.Unmarshal([]byte(supfileContent), &supf)

			if tc.name == "CompletelyEmpty" { // Expecting unmarshal error for totally empty file
				if err == nil {
					t.Errorf("[%s] Expected error for completely empty file, got nil", tc.name)
				} else if !strings.Contains(err.Error(), "failed to unmarshal ansible inventory YAML") {
					// This check might be too specific; any YAML error is fine.
					// It could also be "EOF" from yaml parser.
					t.Logf("[%s] Got expected error for empty file: %v", tc.name, err)
				}
				return // Skip host count check for this error case
			}

			if err != nil {
				t.Fatalf("[%s] Failed to unmarshal Supfile: %v", tc.name, err)
			}

			parsedNet, ok := supf.Networks.Get("testnet")
			if !ok {
				t.Fatalf("[%s] Test network 'testnet' not found", tc.name)
			}

			if len(parsedNet.Hosts) != tc.expectedHosts {
				t.Errorf("[%s] Expected %d hosts, got %d", tc.name, tc.expectedHosts, len(parsedNet.Hosts))
			}
		})
	}
}

// TestAnsibleInventoryPrecedence tests that Ansible inventory takes precedence over HostsFromConfig.
func TestAnsibleInventoryPrecedence(t *testing.T) {
	ansibleInventoryContent := `
all:
  hosts:
    ansible-host1:
    ansible-host2:
`
	ansibleFilePath, cleanupAnsible := createTempInventoryFile(t, ansibleInventoryContent)
	defer cleanupAnsible()

	// Construct Supfile YAML that has both ansible_inventory_file and direct hosts
	supfileYAML := fmt.Sprintf(`
networks:
  precnet:
    ansible_inventory_file: %s
    hosts: # These should be ignored
      - config-host1
      - config-host2
    inventory: "echo inventory-command-host" # This should be ignored/cleared
`, ansibleFilePath)

	var supf Supfile
	err := yaml.Unmarshal([]byte(supfileYAML), &supf)
	if err != nil {
		t.Fatalf("Failed to unmarshal Supfile for precedence test: %v", err)
	}

	parsedNet, ok := supf.Networks.Get("precnet")
	if !ok {
		t.Fatal("Network 'precnet' not found")
	}

	if len(parsedNet.Hosts) != 2 {
		t.Errorf("Expected 2 hosts from Ansible inventory, got %d", len(parsedNet.Hosts))
	}
	if findHost(parsedNet.Hosts, "ansible-host1") == nil {
		t.Error("Host 'ansible-host1' from Ansible inventory not found")
	}
	if findHost(parsedNet.Hosts, "ansible-host2") == nil {
		t.Error("Host 'ansible-host2' from Ansible inventory not found")
	}
	if findHost(parsedNet.Hosts, "config-host1") != nil {
		t.Error("Host 'config-host1' from HostsFromConfig was found, but should have been ignored")
	}

	if parsedNet.Inventory != "" {
		t.Errorf("Expected Network.Inventory to be cleared, but got: '%s'", parsedNet.Inventory)
	}
	if len(parsedNet.HostsFromConfig) != 0 {
		t.Errorf("Expected Network.HostsFromConfig to be empty, but got: %v", parsedNet.HostsFromConfig)
	}
}

// TestParseAnsibleMalformedPort tests how non-integer ansible_port is handled.
// Current implementation of AnsibleHostEntry has AnsiblePort as int, so yaml.v2 will error
// during unmarshal of AnsibleHostEntry if the port is not an int.
// This error occurs before our custom logic, within yaml.Unmarshal -> AnsibleInventoryData.
// The parseAnsibleInventoryFile function should then wrap this error.
func TestParseAnsibleMalformedPort(t *testing.T) {
	inventoryContent := `
all:
  hosts:
    host_bad_port:
      ansible_port: "not-a-number"
`
	filePath, cleanup := createTempInventoryFile(t, inventoryContent)
	defer cleanup()

	supfileContent := fmt.Sprintf(`networks: { testnet: { ansible_inventory_file: "%s" } }`, filePath)
	var supf Supfile
	err := yaml.Unmarshal([]byte(supfileContent), &supf)

	if err == nil {
		t.Fatalf("Expected an error when ansible_port is not an integer, got nil")
	}

	// Check for the specific YAML unmarshal error related to the field type.
	// The error from yaml.v2 might be like: "yaml: unmarshal errors: line X: cannot unmarshal !!str `not-a-number` into int"
	// This error would be wrapped by parseAnsibleInventoryFile's "failed to unmarshal ansible inventory YAML"
	if !strings.Contains(err.Error(), "failed to unmarshal ansible inventory YAML") && !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("Expected error to relate to YAML unmarshalling or type mismatch, got: %v", err)
	}

	// If unmarshalling of AnsibleInventoryData fails, then parsedNet.Hosts should be empty or parsing stops.
	// We don't expect any hosts to be parsed from a malformed file like this.
	parsedNet, ok := supf.Networks.Get("testnet")
	if ok && parsedNet.Hosts != nil && len(parsedNet.Hosts) > 0 {
		t.Errorf("Expected no hosts to be parsed from malformed inventory, but got %d hosts", len(parsedNet.Hosts))
	}
}

// TODO: Add more tests if specific behaviors for nil host entries (e.g. `myhost:`) need deeper verification,
// though current tests (Basic, HostVariables for host1) cover this implicitly.
// TODO: Consider SSH config interactions if NewHost's behavior with SSH config needs to be tested in conjunction
// with Ansible variables. For now, tests focus on direct Ansible var application.

func TestMain(m *testing.M) {
	// Setup code, if any (e.g., mock SSH config for NewHost)
	// For now, NewHost uses actual user.Current() and potentially real SSH config.
	// This is acceptable for these tests as they focus on Ansible parsing part.
	exitCode := m.Run()
	// Teardown code, if any
	os.Exit(exitCode)
}
