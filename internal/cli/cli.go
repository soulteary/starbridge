// Package cli implements the Starbridge command line. Every command except help,
// version, and the MCP server writes exactly one JSON envelope to stdout.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/soulteary/starbridge/internal/bridge"
	"github.com/soulteary/starbridge/internal/contract"
	"github.com/soulteary/starbridge/internal/version"
)

type command struct {
	opts    bridge.Options
	area    string
	action  string
	vm      string
	values  map[string]string
	flags   map[string]bool
	help    bool
	version bool
}

type optionKind uint8

const (
	stringOption optionKind = iota
	intOption
	boolOption
)

// Run executes a CLI invocation and returns its exit status. The caller owns
// cancellation and the supplied writers; Run never exits the process.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd, err := parse(args)
	if err != nil {
		out := stdout
		if cmd.area == "serve" || apparentArea(args) == "serve" {
			out = stderr
		}
		writeResult(out, bridge.Failure(err))
		return 1
	}
	if cmd.version {
		_, err = fmt.Fprintf(stdout, "starbridge %s\n", version.Version)
		return outputStatus(err)
	}
	if cmd.help {
		_, err = io.WriteString(stdout, helpText(cmd))
		return outputStatus(err)
	}
	b, err := bridge.New(cmd.opts)
	if err != nil {
		out := stdout
		if cmd.area == "serve" {
			out = stderr
		}
		writeResult(out, bridge.Failure(err))
		return 1
	}
	if cmd.area == "serve" {
		if cmd.opts.DryRun {
			err = invalid("serve cannot be combined with --dry-run.")
		} else {
			err = runMCP(ctx, b, cmd)
		}
		if err != nil {
			writeResult(stderr, bridge.Failure(err))
			return 1
		}
		return 0
	}
	result, err := dispatch(ctx, cmd, b)
	if err != nil {
		result = bridge.Failure(err)
	}
	if err := writeResult(stdout, result); err != nil {
		return 1
	}
	ok, _ := result["ok"].(bool)
	if !ok {
		return 1
	}
	return 0
}

func outputStatus(err error) int {
	if err != nil {
		return 1
	}
	return 0
}

func writeResult(out io.Writer, result bridge.Result) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(result)
}

func invalid(message string) error {
	return bridge.NewError("INVALID_ARGUMENT", message, nil)
}

// Identify stdio-server invocations even when parsing a global value fails, so
// diagnostics such as an invalid timeout cannot be mistaken for JSON-RPC data.
func apparentArea(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
		name, _, hasValue := splitOption(arg)
		if !hasValue && (name == "prlctl" || name == "timeout" || name == "guest-script" || name == "developer-script") {
			i++
		}
	}
	return ""
}

func parse(args []string) (command, error) {
	c := command{opts: bridge.DefaultOptions(), values: map[string]string{}, flags: map[string]bool{}}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			c.help = true
			return c, nil
		}
		if arg == "--version" {
			c.version = true
			return c, nil
		}
		name, value, hasValue := splitOption(arg)
		switch name {
		case "dry-run":
			if hasValue {
				return c, invalid("--dry-run does not accept a value.")
			}
			c.opts.DryRun = true
		case "prlctl", "guest-script", "developer-script", "timeout":
			if !hasValue {
				i++
				if i >= len(args) || strings.HasPrefix(args[i], "--") || args[i] == "-h" {
					return c, invalid("--" + name + " requires a value.")
				}
				value = args[i]
			}
			switch name {
			case "prlctl":
				c.opts.PRLCTL = value
			case "guest-script":
				c.opts.GuestScript = value
			case "developer-script":
				if value == "" {
					return c, invalid("--developer-script requires a nonempty path.")
				}
				c.opts.DeveloperScript = value
			case "timeout":
				seconds, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 1 || seconds > 600 {
					return c, invalid("timeout must be between 1 and 600 seconds.")
				}
				c.opts.Timeout = time.Duration(seconds * float64(time.Second))
			}
		default:
			return c, invalid("Unknown global option: " + arg)
		}
		i++
	}
	if i >= len(args) {
		return c, invalid("A command is required: doctor, vm, guest, dev, or serve.")
	}
	c.area = args[i]
	i++
	switch c.area {
	case "doctor", "serve":
	case "vm", "guest", "dev":
		if i < len(args) && (args[i] == "--help" || args[i] == "-h") {
			c.help = true
			return c, nil
		}
		if i >= len(args) {
			return c, invalid(c.area + " requires an action.")
		}
		c.action = args[i]
		i++
	default:
		return c, invalid("Unknown command: " + c.area)
	}
	schema, needVM, required, err := commandOptions(c.area, c.action)
	if err != nil {
		return c, err
	}
	positionals := []string{}
	endOptions := false
	for i < len(args) {
		arg := args[i]
		if !endOptions && (arg == "--help" || arg == "-h") {
			c.help = true
			return c, nil
		}
		if !endOptions && arg == "--" {
			endOptions = true
			i++
			continue
		}
		if !endOptions && strings.HasPrefix(arg, "-") {
			name, value, hasValue := splitOption(arg)
			kind, found := schema[name]
			if !found {
				return c, invalid("Unknown option: " + arg)
			}
			if kind == boolOption {
				if hasValue {
					return c, invalid("--" + name + " does not accept a value.")
				}
				c.flags[name] = true
			} else {
				if !hasValue {
					i++
					if i >= len(args) || strings.HasPrefix(args[i], "--") || args[i] == "-h" {
						return c, invalid("--" + name + " requires a value.")
					}
					value = args[i]
				}
				if kind == intOption {
					if _, err := strconv.Atoi(value); err != nil {
						return c, invalid("--" + name + " requires an integer.")
					}
				}
				c.values[name] = value
			}
		} else {
			positionals = append(positionals, arg)
		}
		i++
	}
	if needVM {
		if len(positionals) != 1 {
			return c, invalid("Exactly one VM identifier is required.")
		}
		c.vm = positionals[0]
	} else if len(positionals) != 0 {
		return c, invalid("Unexpected argument: " + positionals[0])
	}
	for _, name := range required {
		if _, found := c.values[name]; !found {
			return c, invalid("--" + name + " is required.")
		}
	}
	if mode, found := c.values["mode"]; found && mode != "stop" && mode != "restore" && mode != "delete" {
		return c, invalid("mode must be stop, restore or delete.")
	}
	// RecycleOptions uses empty strings for omitted options. Preserve CLI
	// presence so blank values cannot silently select another recycle operation.
	if c.area == "vm" && c.action == "recycle" {
		if policy, present := c.values["policy"]; present && strings.TrimSpace(policy) == "" {
			return c, invalid("--policy requires a nonempty file path.")
		}
		if snapshot, present := c.values["snapshot"]; present && snapshot == "" {
			return c, invalid("snapshot ID must be nonempty and cannot start with '-'.")
		}
	}
	return c, nil
}

func splitOption(arg string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "--") {
		return arg, "", false
	}
	name, value, hasValue = strings.Cut(strings.TrimPrefix(arg, "--"), "=")
	return
}

func commandOptions(area, action string) (map[string]optionKind, bool, []string, error) {
	schema := map[string]optionKind{}
	var required []string
	if area == "dev" {
		schema["request"] = stringOption
		schema["allow-write"] = boolOption
		return schema, true, nil, nil
	}
	if area == "doctor" {
		schema["vm"] = stringOption
		return schema, false, nil, nil
	}
	if area == "serve" {
		schema["allow-vm-write"] = boolOption
		schema["developer-tools"] = boolOption
		schema["allow-guest-write"] = boolOption
		schema["developer-workspace"] = stringOption
		schema["access-policy"] = stringOption
		return schema, false, nil, nil
	}
	if area == "vm" {
		switch action {
		case "list":
			return schema, false, nil, nil
		case "info", "start", "suspend", "resume", "snapshot-list":
		case "stop":
			schema["force"] = boolOption
			schema["confirm"] = stringOption
		case "delete":
			schema["confirm"] = stringOption
		case "clone", "snapshot-create":
			schema["name"] = stringOption
			required = []string{"name"}
		case "snapshot-restore":
			schema["snapshot"] = stringOption
			schema["confirm"] = stringOption
			required = []string{"snapshot"}
		case "recycle":
			for _, key := range []string{"policy", "mode", "snapshot", "confirm"} {
				schema[key] = stringOption
			}
		default:
			return nil, false, nil, invalid("Unknown VM action: " + action)
		}
		return schema, true, required, nil
	}
	if area == "guest" {
		switch action {
		case "processes", "windows":
			schema["pid"] = intOption
			schema["limit"] = intOption
			if action == "processes" {
				schema["name"] = stringOption
				schema["include-path"] = boolOption
			} else {
				schema["include-hidden"] = boolOption
			}
		case "ui":
			schema["hwnd"] = stringOption
			for _, key := range []string{"max-depth", "max-nodes", "timeout-ms"} {
				schema[key] = intOption
			}
			required = []string{"hwnd"}
		default:
			return nil, false, nil, invalid("Unknown guest action: " + action)
		}
		return schema, true, required, nil
	}
	return nil, false, nil, invalid("Unknown command.")
}

func (c command) integer(name string, fallback int) int {
	value, present := c.values[name]
	if !present {
		return fallback
	}
	n, _ := strconv.Atoi(value) // parse has already validated every integer.
	return n
}

func dispatch(ctx context.Context, c command, b *bridge.Bridge) (bridge.Result, error) {
	var data any
	var err error
	switch c.area {
	case "dev":
		params, err := developerRequest(c.values)
		if err != nil {
			return nil, err
		}
		return b.Developer(ctx, c.vm, c.action, params, c.flags["allow-write"])
	case "doctor":
		if vm, present := c.values["vm"]; present {
			data, err = b.DoctorVM(ctx, vm)
		} else {
			data, err = b.Doctor(ctx)
		}
	case "vm":
		switch c.action {
		case "list":
			data, err = b.VMList(ctx)
		case "info":
			data, err = b.VMInfo(ctx, c.vm)
		case "clone":
			data, err = b.VMClone(ctx, c.vm, c.values["name"])
		case "snapshot-list":
			data, err = b.SnapshotList(ctx, c.vm)
		case "snapshot-create":
			data, err = b.SnapshotCreate(ctx, c.vm, c.values["name"])
		case "snapshot-restore":
			data, err = b.SnapshotRestore(ctx, c.vm, c.values["snapshot"], c.values["confirm"])
		case "recycle":
			data, err = b.Recycle(ctx, c.vm, bridge.RecycleOptions{
				PolicyFile: c.values["policy"], Mode: c.values["mode"],
				Snapshot: c.values["snapshot"], Confirmation: c.values["confirm"],
			})
		default:
			data, err = b.VMAction(ctx, c.action, c.vm, c.flags["force"], c.values["confirm"])
		}
	case "guest":
		params := map[string]any{}
		action := ""
		if c.action == "ui" {
			action = "ui.tree"
			params["hwnd"] = c.values["hwnd"]
			params["max_depth"] = c.integer("max-depth", contract.DefaultMaxDepth)
			params["max_nodes"] = c.integer("max-nodes", contract.DefaultMaxNodes)
			params["timeout_ms"] = c.integer("timeout-ms", contract.DefaultTimeoutMS)
		} else {
			params["limit"] = c.integer("limit", contract.DefaultListLimit)
			if _, present := c.values["pid"]; present {
				params["pid"] = c.integer("pid", 0)
			}
			if c.action == "processes" {
				action = "processes.list"
				if value, present := c.values["name"]; present {
					params["name"] = value
				}
				params["include_path"] = c.flags["include-path"]
			} else {
				action = "windows.list"
				params["include_hidden"] = c.flags["include-hidden"]
			}
		}
		return b.Guest(ctx, c.vm, action, params)
	default:
		return nil, invalid("Unknown command.")
	}
	if err != nil {
		return nil, err
	}
	return bridge.Success(data), nil
}

func helpText(c command) string {
	if c.area == "" {
		return `Starbridge: AI tools for virtual environments. First backend: Parallels Desktop.

Usage: starbridge [global options] <command> [arguments]

Commands:
  doctor              Inspect host prerequisites without starting a VM
  vm <action>         List, inspect, or manage virtual machines
  guest <action>      Read Windows processes, windows, and UI controls
  dev <action>        Run registered development actions (read-only by default)
  serve               Serve MCP over stdio

Global options (before the command):
  --prlctl PATH        Parallels CLI executable
  --timeout SECONDS    Host command timeout, 1..600 (default 30)
  --guest-script PATH  Installed Windows helper path
  --developer-script PATH  Installed developer helper entry path
  --dry-run            Preview argument vectors without executing
  --version            Print version
  -h, --help           Print help

Use starbridge <command> --help for command details.
All regular commands return a single JSON envelope. MCP uses JSON-RPC over stdio.
`
	}
	if c.area == "vm" && c.action == "" {
		return `Usage: starbridge [global options] vm <action> [arguments]

Actions:
  list
  info VM
  start VM
  stop VM [--force] [--confirm VM]
  suspend VM
  resume VM
  delete VM [--confirm VM]
  clone VM --name NAME
  snapshot-list VM
  snapshot-create VM --name NAME
  snapshot-restore VM --snapshot ID [--confirm VM]
  recycle VM [--policy FILE] [--mode stop|restore|delete] [--snapshot ID] [--confirm VM]

Action options may appear before or after VM. Delete, forced stop, and snapshot
restore require the exact VM identifier in --confirm unless --dry-run is used.
Recycle with --policy requires a VM UUID; display names are rejected even in dry runs.
`
	}
	if c.area == "guest" && c.action == "" {
		return `Usage: starbridge [global options] guest <action> VM [options]

Actions:
  processes VM [--pid PID] [--limit N] [--name NAME] [--include-path]
  windows VM [--pid PID] [--limit N] [--include-hidden]
  ui VM --hwnd HANDLE [--max-depth N] [--max-nodes N] [--timeout-ms N]

Options may appear before or after VM. Defaults: limit=500, max-depth=6,
max-nodes=300, timeout-ms=10000. UI inspection requires an interactive desktop.
`
	}
	switch c.area {
	case "dev":
		return "Usage: starbridge [global options] dev ACTION VM [--request JSONFILE] [--allow-write]\n\nThe request file contains a JSON parameters object; omitted means {}.\nUse system.info to inspect installed developer actions. Mutations require --allow-write.\nDry runs validate parameters and stay offline.\n"
	case "doctor":
		return "Usage: starbridge [global options] doctor [--vm VM]\n\nInspect the host CLI. With --vm, probe guest session, windows and one UI root.\nRead-only; never starts a VM. Inspect data.ready and checks for readiness.\n"
	case "serve":
		return "Usage: starbridge [global options] serve [--allow-vm-write] [--access-policy FILE]\n  [--developer-tools [--allow-guest-write] [--developer-workspace DIR]]\n\nServe MCP over stdio. VM and guest mutations require separate opt-ins.\nDevelopment tools and extended read options require --developer-tools.\nHost file actions require an explicit existing workspace directory.\nOptional access policy limits VMs by UUID and tools by name.\nCannot be combined with --dry-run. Diagnostics go to stderr.\n"
	case "vm":
		if c.action == "recycle" {
			return "Usage: starbridge [global options] vm recycle VM [--policy FILE] [--mode stop|restore|delete] [--snapshot ID] [--confirm VM]\n\nWith --policy, VM and all policy vms keys must be UUIDs, not display names.\nUUID case and paired braces are normalized for matching; --confirm must repeat the original input exactly.\nDry runs stay offline. Without --policy, VM names remain supported.\n"
		}
		usage := map[string]string{
			"list": "list", "info": "info VM", "start": "start VM", "stop": "stop VM [--force] [--confirm VM]",
			"suspend": "suspend VM", "resume": "resume VM", "delete": "delete VM [--confirm VM]",
			"clone": "clone VM --name NAME", "snapshot-list": "snapshot-list VM",
			"snapshot-create":  "snapshot-create VM --name NAME",
			"snapshot-restore": "snapshot-restore VM --snapshot ID [--confirm VM]",
			"recycle":          "recycle VM [--policy FILE] [--mode stop|restore|delete] [--snapshot ID] [--confirm VM]",
		}
		return "Usage: starbridge [global options] vm " + usage[c.action] + "\n\nAction options may appear before or after VM. Use --dry-run before vm to preview.\n"
	case "guest":
		usage := map[string]string{
			"processes": "processes VM [--pid PID] [--limit N] [--name NAME] [--include-path]",
			"windows":   "windows VM [--pid PID] [--limit N] [--include-hidden]",
			"ui":        "ui VM --hwnd HANDLE [--max-depth N] [--max-nodes N] [--timeout-ms N]",
		}
		return "Usage: starbridge [global options] guest " + usage[c.action] + "\n\nOptions may appear before or after VM. UI inspection requires an interactive desktop.\n"
	}
	return ""
}
