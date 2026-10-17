# Project development workflows

`workflow.run` executes a versioned JSON profile against one explicitly selected VM. It requires `--allow-write`, including when its stages only read state. Profiles can start or reset a VM, synchronize files, launch a named task, wait for a condition, assert an outcome, and collect evidence when the corresponding developer modules are installed.

```sh
starbridge dev workflow.run VM --request workflow-request.json --allow-write
```

`workflow-request.json`:

```json
{"profile_file":"/absolute/project/development-profile.json","output_directory":"/absolute/project/runs"}
```

The [example profile](../examples/development-profile.json) contains a synthetic VM UUID and the guest task profile `synthetic-tests`. Replace them explicitly. The guest profile fixes the executable, working directory and permitted arguments; the workflow does not execute arbitrary shell text. The example needs the task module as well as the waiting and assertion actions.

Profiles have `version:1`, a VM UUID, and 1–32 uniquely named `steps`. Each step contains `name`, `action` and optional `params`. Unknown fields, duplicate JSON keys, recursive workflows and unavailable actions fail profile loading. Optional `developer_script` selects an absolute Windows helper path; `timeout_seconds` sets the per-command timeout, up to 600 seconds. The calling context still controls cancellation of the complete run.

The VM UUID in the profile must match the requested VM. All stages target that VM. `vm.clone` can create another VM, but subsequent stages continue to target the original; use a separate profile to operate the clone.

## References and failure handling

Use an object containing only `{"$ref":"earlier.data.task_id"}` to insert a prior step's result into a later parameter. Dot paths support object fields and array indexes. Forward references and missing fields fail the affected stage. References do not interpolate into strings.

Each run creates a random subdirectory with restrictive permissions. Stage result files and `report.json` record outcomes, durations and the profile SHA-256 without copying profile inputs. Returned observations can themselves contain selected application data; choose the profile's queries and output directory accordingly. Failed runs retain these checkpoints and stop before the next stage. Mutations are never automatically retried.

The optional `cleanup` must be an explicit `vm.recycle` action with mode `stop`, `restore` or `delete`. It runs after successful stages or a stage failure while the context remains usable. Cancellation leaves cleanup `pending`; inspect the report before invoking `workflow.cleanup` with the same request to run cleanup separately. Cleanup is a new run and does not resume failed stages.

Snapshot restore and recycle modes `restore`/`delete` require request `confirm_vm` to repeat the requested VM exactly. The workflow checks literal destructive cleanup and restore steps before the first stage; the underlying VM operation checks confirmation again when executed. Cleanup is absent unless configured. A cleanup failure is a failed workflow with a retained report.

## Validation

Host tests use fake VM commands, synthetic UUIDs and temporary directories. They cover result references, checkpoints, failure stop behavior, target matching and destructive-operation confirmation. They do not contact Parallels or a real desktop. Module-specific Windows contracts and explicit desktop acceptance provide separate coverage.
