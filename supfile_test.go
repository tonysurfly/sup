package sup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Helper function to set up the mock ansible-inventory environment
func setupMockAnsibleInventory(t *testing.T, action, jsonContent, stderrMsg, exitCodeStr, envCaptureFile string) (mockDir string, cleanup func()) {
	t.Helper()

	// Create a temporary directory for the mock script
	mockDir, err := os.MkdirTemp("", "mock_ansible_inventory_")
	if err != nil {
		t.Fatalf("Failed to create temp dir for mock script: %v", err)
	}

	mockScriptPath := filepath.Join(mockDir, "ansible-inventory")
	// On Windows, executable needs .exe extension
	if os.PathSeparator == '\\' {
		mockScriptPath += ".exe"
	}

	// Compile the mock_ansible_inventory.go program
	// Assuming mock_ansible_inventory.go is in the same directory as supfile_test.go or project root
	// and tests are run from that directory.
	cmd := exec.Command("go", "build", "-o", mockScriptPath, "./mock_ansible_inventory.go")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(mockDir)
		t.Fatalf("Failed to compile mock_ansible_inventory.go: %v", err)
	}

	// Store original PATH and defer restoration
	originalPath := os.Getenv("PATH")
	os.Setenv("PATH", mockDir+string(os.PathListSeparator)+originalPath)

	// Set environment variables for the mock script
	os.Setenv("MOCK_AI_ACTION", action)
	if jsonContent != "" {
		os.Setenv("MOCK_AI_JSON_CONTENT", jsonContent)
	}
	if stderrMsg != "" {
		os.Setenv("MOCK_AI_STDERR_MSG", stderrMsg)
	}
	if exitCodeStr != "" {
		os.Setenv("MOCK_AI_EXIT_CODE", exitCodeStr)
	}
	if envCaptureFile != "" {
		os.Setenv("MOCK_AI_ENV_CAPTURE_FILE", envCaptureFile)
	}

	cleanup = func() {
		os.Setenv("PATH", originalPath)
		os.Unsetenv("MOCK_AI_ACTION")
		os.Unsetenv("MOCK_AI_JSON_CONTENT")
		os.Unsetenv("MOCK_AI_STDERR_MSG")
		os.Unsetenv("MOCK_AI_EXIT_CODE")
		os.Unsetenv("MOCK_AI_ENV_CAPTURE_FILE")
		os.RemoveAll(mockDir)
	}

	return mockDir, cleanup
}

func TestParseAnsibleInventory_Success(t *testing.T) {
	tests := []struct {
		name           string
		mockJSON       string
		expectedHosts  []string
		inventoryPath  string // if empty, a dummy one will be created
		supEnv         EnvList
	}{
		{
			name: "Simple host list",
			mockJSON: `{
				"webservers": {
					"hosts": ["host1.example.com", "host2.example.com"]
				},
				"_meta": {"hostvars": {}}
			}`,
			expectedHosts: []string{"host1.example.com", "host2.example.com"},
		},
		{
			name: "Nested groups",
			mockJSON: `{
				"all": {
					"children": ["parentgroup"]
				},
				"parentgroup": {
					"children": ["childgroup"]
				},
				"childgroup": {
					"hosts": ["nestedhost1.example.com"]
				},
				"_meta": {"hostvars": {}}
			}`,
			expectedHosts: []string{"nestedhost1.example.com"},
		},
		{
			name: "Multiple groups and duplicate hosts",
			mockJSON: `{
				"web": {"hosts": ["h1", "h2"]},
				"db": {"hosts": ["h2", "h3"]},
				"_meta": {"hostvars": {}}
			}`,
			expectedHosts: []string{"h1", "h2", "h3"},
		},
		{
			name: "Empty groups and groups with no hosts",
			mockJSON: `{
				"emptygroup": {},
				"nohostsgroup": {"children": ["anothergroup"]},
				"anothergroup": {"hosts": ["h1"]},
				"_meta": {"hostvars": {}}
			}`,
			expectedHosts: []string{"h1"},
		},
		{
			name: "No 'all' group, direct top-level groups",
			mockJSON: `{
				"group1": {"hosts": ["hostA"]},
				"group2": {"hosts": ["hostB"]},
				"_meta": {"hostvars": {}}
			}`,
			expectedHosts: []string{"hostA", "hostB"},
		},
		{
			name: "Realistic output with _meta and ungrouped",
			mockJSON: `{
				"_meta": {
					"hostvars": {
						"host1.example.com": {"ansible_host": "192.168.1.10"}
					}
				},
				"all": {
					"children": ["webservers", "ungrouped"]
				},
				"webservers": {
					"hosts": ["host1.example.com", "host2.example.com"]
				},
				"ungrouped": {
					"hosts": ["host3.example.com"]
				}
			}`,
			expectedHosts: []string{"host1.example.com", "host2.example.com", "host3.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cleanup := setupMockAnsibleInventory(t, "output_json", tt.mockJSON, "", "", "")
			defer cleanup()

			inventoryFilePath := tt.inventoryPath
			if inventoryFilePath == "" {
				tmpFile, err := os.CreateTemp("", "test_inventory_*.yml")
				if err != nil {
					t.Fatalf("Failed to create temp inventory file: %v", err)
				}
				inventoryFilePath = tmpFile.Name()
				tmpFile.Close()
				defer os.Remove(inventoryFilePath)
			}

			hosts, err := ParseAnsibleInventory(inventoryFilePath, tt.supEnv)
			if err != nil {
				t.Fatalf("ParseAnsibleInventory() error = %v, wantErr nil", err)
			}

			var gotHostnames []string
			for _, h := range hosts {
				gotHostnames = append(gotHostnames, h.GetHostname())
			}
			sort.Strings(gotHostnames)
			sort.Strings(tt.expectedHosts)

			if !reflect.DeepEqual(gotHostnames, tt.expectedHosts) {
				t.Errorf("ParseAnsibleInventory() got hosts = %v, want %v", gotHostnames, tt.expectedHosts)
			}
		})
	}
}

func TestParseAnsibleInventory_Errors(t *testing.T) {
	tests := []struct {
		name             string
		action           string // for mock script
		mockJSON         string // only for output_json
		stderrMsg        string
		exitCode         string
		supEnv           EnvList
		inventoryPath    string // if empty, a dummy one will be created
		expectedErrSubstring string
	}{
		{
			name:          "Command fails with non-zero exit",
			action:        "exit_error",
			exitCode:      "1",
			stderrMsg:     "ansible-inventory command failed",
			expectedErrSubstring: "failed to execute ansible-inventory",
		},
		{
			name:          "Command fails with stderr output",
			action:        "stderr_output",
			stderrMsg:     "some critical error from script",
			exitCode:      "1", // often errors also result in non-zero exit
			expectedErrSubstring: "some critical error from script",
		},
		{
			name:          "Invalid JSON output",
			action:        "output_json",
			mockJSON:      `{"webservers": {"hosts": ["host1"}}`, // Malformed JSON
			expectedErrSubstring: "failed to unmarshal ansible-inventory JSON output",
		},
		{
			name:          "Empty inventory path",
			action:        "output_json", // Action doesn't matter as it should fail before cmd execution
			inventoryPath: "",
			expectedErrSubstring: "ansible inventory path is empty",
		},
		{
			name:          "Host creation fails (e.g. invalid hostname)",
			action:        "output_json",
			mockJSON:      `{"group": {"hosts": ["host/with/slash"]}}`,
			expectedErrSubstring: "failed to create host object for 'host/with/slash'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cleanup := setupMockAnsibleInventory(t, tt.action, tt.mockJSON, tt.stderrMsg, tt.exitCode, "")
			defer cleanup()

			inventoryFilePath := tt.inventoryPath
			// For the "Empty inventory path" test, inventoryPath will be ""
			if tt.name != "Empty inventory path" && inventoryFilePath == "" {
				tmpFile, err := os.CreateTemp("", "test_inventory_*.yml")
				if err != nil {
					t.Fatalf("Failed to create temp inventory file: %v", err)
				}
				inventoryFilePath = tmpFile.Name()
				tmpFile.Close()
				defer os.Remove(inventoryFilePath)
			}


			_, err := ParseAnsibleInventory(inventoryFilePath, tt.supEnv)
			if err == nil {
				t.Fatalf("ParseAnsibleInventory() expected an error, but got nil")
			}
			if !strings.Contains(err.Error(), tt.expectedErrSubstring) {
				t.Errorf("ParseAnsibleInventory() error = %q, want substring %q", err.Error(), tt.expectedErrSubstring)
			}
		})
	}
}

func TestParseAnsibleInventory_EnvPassing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "env_test_capture_")
	if err != nil {
		t.Fatalf("Failed to create temp dir for env capture: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	envCaptureFile := filepath.Join(tmpDir, "captured_env.txt")

	_, cleanup := setupMockAnsibleInventory(t, "capture_env", "", "", "", envCaptureFile)
	defer cleanup()

	supEnv := EnvList{
		{Key: "SUP_TEST_VAR", Value: "sup_value"},
		{Key: "ANOTHER_VAR", Value: "another_sup_value"},
	}

	// Create a dummy inventory file
	tmpInventoryFile, err := os.CreateTemp("", "dummy_inventory_*.yml")
	if err != nil {
		t.Fatalf("Failed to create temp inventory file: %v", err)
	}
	inventoryPath := tmpInventoryFile.Name()
	tmpInventoryFile.Close()
	defer os.Remove(inventoryPath)

	_, err = ParseAnsibleInventory(inventoryPath, supEnv)
	if err != nil {
		// The mock script with "capture_env" should exit 0 if it can write the file
		t.Fatalf("ParseAnsibleInventory() with env capture returned error: %v", err)
	}

	capturedEnvData, err := os.ReadFile(envCaptureFile)
	if err != nil {
		t.Fatalf("Failed to read captured env file %s: %v", envCaptureFile, err)
	}

	capturedEnvStr := string(capturedEnvData)
	for _, envVar := range supEnv {
		expectedEnv := fmt.Sprintf("%s=%s", envVar.Key, envVar.Value)
		if !strings.Contains(capturedEnvStr, expectedEnv) {
			t.Errorf("Expected env var %q not found in captured environment:\n%s", expectedEnv, capturedEnvStr)
		}
	}
	// Check if PATH was correctly modified to include mockDir (from setup function)
	// This is a bit meta, but useful
	if !strings.Contains(capturedEnvStr, "PATH="+os.Getenv("PATH")) {
		// Note: os.Getenv("PATH") here will be the modified path
		t.Errorf("Expected modified PATH not found or incorrect in captured environment.\nCaptured PATH might not reflect the one set for the child process if not inherited fully by mock script logic or capture method.")
	}
}

// Example of how to use json.RawMessage for more complex JSON structures if needed in future
type AnsibleInventoryGroupRaw struct {
	Hosts    json.RawMessage `json:"hosts"`    // Can be []string or map[string]interface{}
	Children json.RawMessage `json:"children"` // Can be []string
	Vars     json.RawMessage `json:"vars"`     // Can be map[string]interface{}
}
func TestMain(m *testing.M) {
	// This check ensures that mock_ansible_inventory.go is present at the expected relative path
	// before any tests try to compile it.
	// Assuming mock_ansible_inventory.go is in the same directory as supfile_test.go or project root.
	if _, err := os.Stat("./mock_ansible_inventory.go"); os.IsNotExist(err) {
		// Fallback for cases where tests might be run from a different working dir than the package dir.
		// This is less ideal, direct path is better.
		altPath := "mock_ansible_inventory.go"
		if _, err2 := os.Stat(altPath); os.IsNotExist(err2) {
			fmt.Fprintf(os.Stderr, "Error: mock_ansible_inventory.go not found at ./mock_ansible_inventory.go or %s. Make sure it's in the correct directory.\n", altPath)
			os.Exit(1)
		}
	}
	// Potentially compile mock script once here if it's static and doesn't need per-test compilation
	// For now, each test setup compiles it to ensure a clean state.
	os.Exit(m.Run())
}
