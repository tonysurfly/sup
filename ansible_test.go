package sup

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseAnsibleInventoryJSON(t *testing.T) {
	// Example JSON output from ansible-inventory command
	sampleJSON := `{
		"_meta": {
			"hostvars": {
				"192.168.3.3": {
					"common_var": "this is common to all"
				},
				"db1.example.com": {
					"common_var": "this is common to all"
				},
				"utility1.example.com": {
					"common_var": "this is common to all"
				},
				"web1.example.com": {
					"common_var": "this is common to all"
				},
				"web2.example.com": {
					"ansible_host": "192.168.1.102",
					"common_var": "this is common to all"
				}
			}
		},
		"all": {
			"children": [
				"ungrouped",
				"webservers",
				"dbservers"
			]
		},
		"dbservers": {
			"hosts": [
				"db1.example.com"
			]
		},
		"ungrouped": {
			"hosts": [
				"utility1.example.com",
				"192.168.3.3"
			]
		},
		"webservers": {
			"hosts": [
				"web1.example.com",
				"web2.example.com"
			]
		}
	}`

	// Parse the JSON into our AnsibleInventory struct
	var inventory AnsibleInventory
	err := json.Unmarshal([]byte(sampleJSON), &inventory)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Test expected values
	expectedChildrenGroups := []string{"ungrouped", "webservers", "dbservers"}
	if !reflect.DeepEqual(inventory.All.Children, expectedChildrenGroups) {
		t.Errorf("Expected children groups %v, got %v", expectedChildrenGroups, inventory.All.Children)
	}

	// Test that we can process the JSON properly for host extraction
	var unmarshalledData map[string]interface{}
	err = json.Unmarshal([]byte(sampleJSON), &unmarshalledData)
	if err != nil {
		t.Fatalf("Failed to parse JSON into map: %v", err)
	}

	// Extract hosts from groups
	hostMap := make(map[string]bool)
	for groupName, groupData := range unmarshalledData {
		if groupName == "_meta" || groupName == "all" {
			continue
		}

		groupMap, ok := groupData.(map[string]interface{})
		if !ok {
			continue
		}

		hostsArray, ok := groupMap["hosts"].([]interface{})
		if !ok {
			continue
		}

		for _, hostEntry := range hostsArray {
			host, ok := hostEntry.(string)
			if ok {
				hostMap[host] = true
			}
		}
	}

	// Expected hosts from all groups
	expectedHosts := []string{
		"db1.example.com",
		"utility1.example.com",
		"192.168.3.3",
		"web1.example.com",
		"web2.example.com",
	}

	// Check that all expected hosts are present
	for _, host := range expectedHosts {
		if !hostMap[host] {
			t.Errorf("Expected host %s to be present in extracted hosts", host)
		}
	}

	// Check that the number of hosts is correct
	if len(hostMap) != len(expectedHosts) {
		t.Errorf("Expected %d hosts, got %d", len(expectedHosts), len(hostMap))
	}

	// Test that ansible_host values are correctly extracted
	hostVars := inventory.Meta.Hostvars
	if ansibleHost, ok := hostVars["web2.example.com"]["ansible_host"].(string); !ok || ansibleHost != "192.168.1.102" {
		t.Errorf("Expected ansible_host for web2.example.com to be 192.168.1.102, got %v", hostVars["web2.example.com"]["ansible_host"])
	}
}
