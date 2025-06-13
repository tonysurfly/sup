package sup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/pkg/errors"
)

// parseAnsibleInventory runs the ansible-inventory command and parses its JSON output
// to get hosts from the inventory file
func (n Network) parseAnsibleInventory() ([]*Host, error) {
	cmd := exec.Command("ansible-inventory", "-i", n.AnsibleInventory, "--list")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, n.Env.Slice()...)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.Wrap(err, "running ansible-inventory command failed")
	}

	var inventoryData map[string]interface{}
	err = json.Unmarshal(output, &inventoryData)
	if err != nil {
		return nil, errors.Wrap(err, "parsing ansible inventory JSON failed")
	}

	hostMap := make(map[string]bool)
	if err := collectHostsFromGroup(n.AnsibleGroup, inventoryData, hostMap); err != nil {
		return nil, fmt.Errorf("error processing ansible group '%s': %v", n.AnsibleGroup, err)
	}

	if len(hostMap) == 0 {
		return nil, fmt.Errorf("no hosts found in ansible group '%s' or its children", n.AnsibleGroup)
	}

	// Convert hostnames to Host objects
	var hosts []*Host
	for hostname := range hostMap {
		host, err := NewHost(hostname)
		if err != nil {
			return nil, errors.Wrap(err, fmt.Sprintf("failed to create host from %s", hostname))
		}
		hosts = append(hosts, host)
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts found in ansible group '%s' or its children", n.AnsibleGroup)
	}

	return hosts, nil
}

// collectHostsFromGroup collects all hosts from a group and its child groups recursively
func collectHostsFromGroup(groupName string, inventoryData map[string]interface{}, hostMap map[string]bool) error {
	// Look for the specified group
	groupData, ok := inventoryData[groupName]
	if !ok {
		return fmt.Errorf("group '%s' not found in inventory", groupName)
	}

	groupMap, ok := groupData.(map[string]interface{})
	if !ok {
		return fmt.Errorf("group '%s' has invalid format", groupName)
	}

	if hostsArray, ok := groupMap["hosts"].([]interface{}); ok {
		for _, entry := range hostsArray {
			if hostname, ok := entry.(string); ok {
				hostMap[hostname] = true
			}
		}
	}

	// Process child groups directly and recursively
	if childrenArray, ok := groupMap["children"].([]interface{}); ok {
		for _, entry := range childrenArray {
			if childGroupName, ok := entry.(string); ok {
				if err := collectHostsFromGroup(childGroupName, inventoryData, hostMap); err != nil {
					fmt.Fprintf(os.Stderr, "error processing child group '%s': %v\n", childGroupName, err)
					os.Exit(1)
				}
			}
		}
	}

	return nil
}
