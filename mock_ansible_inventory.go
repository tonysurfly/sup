package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	action := os.Getenv("MOCK_AI_ACTION")

	switch action {
	case "output_json":
		jsonContent := os.Getenv("MOCK_AI_JSON_CONTENT")
		if jsonContent == "" {
			fmt.Fprintln(os.Stderr, "Error: MOCK_AI_JSON_CONTENT not set for output_json action")
			os.Exit(1)
		}
		fmt.Println(jsonContent)
		os.Exit(0)
	case "exit_error":
		exitCodeStr := os.Getenv("MOCK_AI_EXIT_CODE")
		if exitCodeStr == "" {
			exitCodeStr = "1" // Default exit code
		}
		exitCode, err := strconv.Atoi(exitCodeStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Invalid MOCK_AI_EXIT_CODE: %v\n", err)
			os.Exit(1)
		}
		stderrMsg := os.Getenv("MOCK_AI_STDERR_MSG")
		if stderrMsg != "" {
			fmt.Fprintln(os.Stderr, stderrMsg)
		}
		os.Exit(exitCode)
	case "stderr_output":
		stderrMsg := os.Getenv("MOCK_AI_STDERR_MSG")
		if stderrMsg == "" {
			fmt.Fprintln(os.Stderr, "Error: MOCK_AI_STDERR_MSG not set for stderr_output action")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, stderrMsg)
		exitCodeStr := os.Getenv("MOCK_AI_EXIT_CODE")
		if exitCodeStr != "" {
			exitCode, err := strconv.Atoi(exitCodeStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Invalid MOCK_AI_EXIT_CODE: %v\n", err)
				os.Exit(1) // Exit with 1 if code is invalid
			}
			os.Exit(exitCode)
		}
		os.Exit(0)
	case "capture_env":
		captureFile := os.Getenv("MOCK_AI_ENV_CAPTURE_FILE")
		if captureFile == "" {
			fmt.Fprintln(os.Stderr, "Error: MOCK_AI_ENV_CAPTURE_FILE not set for capture_env action")
			os.Exit(1)
		}
		file, err := os.Create(captureFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not create capture file %s: %v\n", captureFile, err)
			os.Exit(1)
		}
		defer file.Close()
		for _, envVar := range os.Environ() {
			_, err := file.WriteString(envVar + "\n")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not write to capture file %s: %v\n", captureFile, err)
				// Don't exit immediately, try to write as much as possible
			}
		}
		os.Exit(0)
	default:
		// Simulates a real ansible-inventory if no MOCK_AI_ACTION is set,
		// or if the inventory file path is the primary argument.
		// This part is tricky to make work universally for tests without a real inventory file.
		// For strict mocking, we should always require MOCK_AI_ACTION.
		// If an inventory file path is passed as an argument (e.g. -i <path>), print it.
		args := os.Args
		inventoryPath := ""
		for i, arg := range args {
			if arg == "-i" && i+1 < len(args) {
				inventoryPath = args[i+1]
				break
			}
		}
		if inventoryPath != "" {
			// In a real test, we might want to read this file and output its content
			// or a default JSON if the file is specific. For now, just acknowledge.
			// fmt.Fprintf(os.Stderr, "Mock ansible-inventory called with inventory path: %s\n", inventoryPath)
			// Fallback to a very basic valid JSON if no specific action is provided,
			// assuming the test might not always set MOCK_AI_ACTION for simple path checks.
			fmt.Println(`{"_meta": {"hostvars": {}}}`)
			os.Exit(0)
		} else {
			fmt.Fprintln(os.Stderr, "Error: MOCK_AI_ACTION not set or unrecognized arguments")
			os.Exit(1)
		}
	}
}
