import type { CustomHeader } from '@/api/endpoints/channel';

export type UAPreset = { id: string; label: string; headers: CustomHeader[] };

export const UA_PRESETS: UAPreset[] = [
    ...['2.1.207', '2.1.206', '2.1.205'].map((version) => ({
        id: `claude-${version}`,
        label: `Claude Code ${version}`,
        headers: [
            { header_key: 'User-Agent', header_value: `claude-cli/${version} (external, cli)` },
            { header_key: 'x-app', header_value: 'cli' },
        ],
    })),
    ...['0.144.3', '0.144.2', '0.144.1'].map((version) => ({
        id: `codex-${version}`,
        label: `Codex CLI ${version}`,
        headers: [
            { header_key: 'User-Agent', header_value: `codex_cli_rs/${version}` },
            { header_key: 'originator', header_value: 'codex_cli_rs' },
        ],
    })),
    ...['0.18.2', '0.18.1', '0.18.0'].map((version) => ({
        id: `hermes-${version}`,
        label: `Hermes Agent ${version}`,
        headers: [{ header_key: 'User-Agent', header_value: `hermes-agent/${version}` }],
    })),
];

export function mergePresetHeaders(current: CustomHeader[], preset: UAPreset): CustomHeader[] {
    const next = [...current];
    for (const header of preset.headers) {
        const index = next.findIndex((item) => item.header_key.toLowerCase() === header.header_key.toLowerCase());
        if (index >= 0) next[index] = header;
        else next.push(header);
    }
    return next;
}
