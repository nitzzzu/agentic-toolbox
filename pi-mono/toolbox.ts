/**
 * toolbox.ts — pi-mono extension
 *
 * Overrides the four core tools (bash, read, write, edit) to route execution
 * through toolbox, giving the agent full container isolation with zero changes
 * to how it thinks or what it calls.
 *
 * Usage:
 *   pi -e ./toolbox.ts
 *   pi -e ./toolbox.ts --toolbox-container browser   # force a specific container
 *   pi -e ./toolbox.ts --toolbox-bin /path/to/toolbox  # explicit binary path
 *
 * Requirements:
 *   - toolbox CLI installed and on PATH (or pass --toolbox-bin)
 *   - .toolbox/catalog.yaml in the workspace (run `toolbox init` to create)
 *   - Containers started (run `toolbox up` before starting pi)
 *
 * How it works:
 *   bash  → spawnHook rewrites every command to `toolbox exec "<command>"`
 *   read  → ReadOperations.readFile runs `toolbox exec "cat <file>"` inside container
 *   write → WriteOperations.writeFile pipes content via `toolbox exec "tee <file>"`
 *   edit  → EditOperations.readFile/writeFile do the same via container
 *
 *   The workspace directory on the host is mounted as /workspace in every
 *   container, so files written by read/write/edit are visible to the agent
 *   and to every tool container without any copying.
 */

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import {
	type EditOperations,
	type ReadOperations,
	type WriteOperations,
	createBashTool,
	createEditTool,
	createReadTool,
	createWriteTool,
} from "@mariozechner/pi-coding-agent";

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

interface ToolboxConfig {
	/** Force all commands to a specific named container (skips routing) */
	container?: string;
	/** Path to toolbox binary (default: "toolbox" on PATH) */
	binary: string;
	/** Workspace root — where .toolbox/ lives and volumes are mounted from */
	cwd: string;
}

function loadConfig(): ToolboxConfig {
	const args = process.argv;

	const containerIdx = args.indexOf("--toolbox-container");
	const container =
		containerIdx !== -1 ? args[containerIdx + 1] : undefined;

	const binIdx = args.indexOf("--toolbox-bin");
	const binary = binIdx !== -1 ? args[binIdx + 1] : "toolbox";

	return {
		container,
		binary,
		cwd: process.cwd(),
	};
}

// ---------------------------------------------------------------------------
// Core exec primitive
// ---------------------------------------------------------------------------

function toolboxExec(
	config: ToolboxConfig,
	args: string[],
	input?: string,
): Promise<{ output: string; exitCode: number }> {
	return new Promise((resolve) => {
		const child = spawn(config.binary, args, {
			cwd: config.cwd,
			env: process.env,
			stdio: input !== undefined ? ["pipe", "pipe", "pipe"] : ["ignore", "pipe", "pipe"],
		});

		let output = "";
		child.stdout.on("data", (d: Buffer) => { output += d.toString(); });
		child.stderr.on("data", (d: Buffer) => { output += d.toString(); });

		if (input !== undefined && child.stdin) {
			child.stdin.write(input);
			child.stdin.end();
		}

		child.on("error", (err) => {
			resolve({ output: err.message, exitCode: 1 });
		});

		child.on("close", (code) => {
			resolve({ output, exitCode: code ?? 0 });
		});
	});
}

function execArgs(config: ToolboxConfig, command: string): string[] {
	const args = ["exec"];
	if (config.container) {
		args.push("--container", config.container);
	}
	args.push(command);
	return args;
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

/**
 * Convert an absolute host path to its /workspace equivalent inside the container.
 *
 * Host:      /home/user/projects/my-app/src/index.ts   (or C:\...\my-app\src\index.ts)
 * Workspace: /home/user/projects/my-app                (= config.cwd)
 * Container: /workspace/src/index.ts
 */
function hostToContainer(workspaceCwd: string, hostPath: string): string {
	const abs = path.resolve(hostPath);
	const rel = path.relative(workspaceCwd, abs);
	return "/workspace/" + rel.split(path.sep).join("/");
}

function shellEscape(str: string): string {
	return `'${str.replace(/'/g, "'\\''")}'`;
}

// ---------------------------------------------------------------------------
// Bash tool — spawnHook rewrites every command to `toolbox exec "<cmd>"`
// ---------------------------------------------------------------------------

function makeBashTool(config: ToolboxConfig) {
	return createBashTool(config.cwd, {
		spawnHook: ({ command, env }) => {
			const args = [config.binary, "exec"];
			if (config.container) {
				args.push("--container", config.container);
			}
			// shellEscape wraps the full command in single quotes so that shell
			// operators like && | ; inside the original command are passed as a
			// single argument to `toolbox exec` rather than being evaluated by
			// the parent shell spawned by pi.
			args.push(shellEscape(command));
			return {
				command: args.join(" "),
				cwd: config.cwd,
				env: { ...env, ...process.env },
			};
		},
	});
}

// ---------------------------------------------------------------------------
// Read tool — ReadOperations: readFile + access
// ---------------------------------------------------------------------------

function makeReadOperations(config: ToolboxConfig): ReadOperations {
	return {
		readFile: async (absolutePath: string): Promise<Buffer> => {
			const containerPath = hostToContainer(config.cwd, absolutePath);
			const { output, exitCode } = await toolboxExec(
				config,
				execArgs(config, `cat ${shellEscape(containerPath)}`),
			);
			if (exitCode !== 0) {
				throw new Error(`read failed (exit ${exitCode}): ${output}`);
			}
			return Buffer.from(output);
		},
		access: async (absolutePath: string): Promise<void> => {
			const containerPath = hostToContainer(config.cwd, absolutePath);
			const { exitCode, output } = await toolboxExec(
				config,
				execArgs(config, `test -r ${shellEscape(containerPath)}`),
			);
			if (exitCode !== 0) {
				throw new Error(`file not readable: ${absolutePath} (${output.trim()})`);
			}
		},
	};
}

// ---------------------------------------------------------------------------
// Write tool — WriteOperations: writeFile + mkdir
// ---------------------------------------------------------------------------

function makeWriteOperations(config: ToolboxConfig): WriteOperations {
	return {
		writeFile: async (absolutePath: string, content: string): Promise<void> => {
			const containerPath = hostToContainer(config.cwd, absolutePath);
			const { exitCode, output } = await toolboxExec(
				config,
				execArgs(config, `tee ${shellEscape(containerPath)}`),
				content,
			);
			if (exitCode !== 0) {
				throw new Error(`write failed (exit ${exitCode}): ${output}`);
			}
		},
		mkdir: async (dir: string): Promise<void> => {
			const containerPath = hostToContainer(config.cwd, dir);
			const { exitCode, output } = await toolboxExec(
				config,
				execArgs(config, `mkdir -p ${shellEscape(containerPath)}`),
			);
			if (exitCode !== 0) {
				throw new Error(`mkdir failed (exit ${exitCode}): ${output}`);
			}
		},
	};
}

// ---------------------------------------------------------------------------
// Edit tool — EditOperations: readFile + writeFile + access
// ---------------------------------------------------------------------------

function makeEditOperations(config: ToolboxConfig): EditOperations {
	return {
		readFile: async (absolutePath: string): Promise<Buffer> => {
			const containerPath = hostToContainer(config.cwd, absolutePath);
			const { output, exitCode } = await toolboxExec(
				config,
				execArgs(config, `cat ${shellEscape(containerPath)}`),
			);
			if (exitCode !== 0) {
				throw new Error(`read failed (exit ${exitCode}): ${output}`);
			}
			return Buffer.from(output);
		},
		writeFile: async (absolutePath: string, content: string): Promise<void> => {
			const containerPath = hostToContainer(config.cwd, absolutePath);
			const { exitCode, output } = await toolboxExec(
				config,
				execArgs(config, `tee ${shellEscape(containerPath)}`),
				content,
			);
			if (exitCode !== 0) {
				throw new Error(`write failed (exit ${exitCode}): ${output}`);
			}
		},
		access: async (absolutePath: string): Promise<void> => {
			const containerPath = hostToContainer(config.cwd, absolutePath);
			// Use POSIX-compatible separate flags; `test -rw` is bash-only and
			// fails in containers that run /bin/sh.
			const { exitCode, output } = await toolboxExec(
				config,
				execArgs(config, `[ -r ${shellEscape(containerPath)} ] && [ -w ${shellEscape(containerPath)} ]`),
			);
			if (exitCode !== 0) {
				throw new Error(`file not accessible: ${absolutePath} (${output.trim()})`);
			}
		},
	};
}

// ---------------------------------------------------------------------------
// Sanity check
// ---------------------------------------------------------------------------

async function sanityCheck(
	config: ToolboxConfig,
	ctx: { ui: { notify: (msg: string, level: string) => void } },
): Promise<void> {
	console.error(`[toolbox] sanity check starting, binary="${config.binary}", cwd="${config.cwd}"`);
	const { exitCode } = await toolboxExec(config, ["--version"]);
	if (exitCode !== 0) {
		const msg = `toolbox binary not found at "${config.binary}". Install toolbox and ensure it is on PATH, or pass --toolbox-bin.`;
		ctx.ui.notify(msg, "error");
		console.error(`[toolbox] ERROR: ${msg}`);
		return;
	}

	const catalogPath = path.join(config.cwd, ".toolbox", "catalog.yaml");
	if (!fs.existsSync(catalogPath)) {
		ctx.ui.notify(
			`No .toolbox/catalog.yaml found in ${config.cwd}. ` +
			`Run \`toolbox init\` to create one, then \`toolbox up\` to start containers.`,
			"warning",
		);
		return;
	}

	const { output, exitCode: statusCode } = await toolboxExec(config, ["status"]);
	if (statusCode !== 0 || output.includes("No toolbox containers")) {
		ctx.ui.notify(
			`Toolbox containers are not running. Run \`toolbox up\` in your workspace first.`,
			"warning",
		);
		return;
	}

	const containerInfo = config.container
		? ` (forced container: ${config.container})`
		: " (auto-routing enabled)";

	const msg = `toolbox ready${containerInfo}. All tools are container-isolated.`;
	ctx.ui.notify(msg, "info");
	console.error(`[toolbox] ${msg}`);
}

// ---------------------------------------------------------------------------
// Extension entry point
// ---------------------------------------------------------------------------

export default function (pi: ExtensionAPI) {
	// Tools must be registered synchronously here (at module load time) so that
	// AgentSession._buildRuntime() picks them up via getAllRegisteredTools() when
	// it builds the tool registry in the constructor — before session_start fires.
	// Registering inside session_start is too late: the tool registry is already
	// built by then and the base bash/read/write/edit tools would be used instead.
	const config = loadConfig();

	pi.registerTool(makeBashTool(config));
	pi.registerTool(createReadTool(config.cwd, { operations: makeReadOperations(config) }));
	pi.registerTool(createWriteTool(config.cwd, { operations: makeWriteOperations(config) }));
	pi.registerTool(createEditTool(config.cwd, { operations: makeEditOperations(config) }));

	// Async sanity check runs on session_start where ctx.ui.notify is available.
	pi.on("session_start", async (_event, ctx) => {
		await sanityCheck(config, ctx);
	});
}
