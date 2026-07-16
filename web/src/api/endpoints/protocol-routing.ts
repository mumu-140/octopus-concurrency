import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';

export type Protocol = 'openai_chat' | 'openai_response' | 'anthropic';
export type RoutingMode = 'legacy' | 'observe' | 'adaptive';
export type PolicyMode = 'inherit' | 'auto' | 'prefer' | 'force';

export interface BaseUrl { url: string; delay: number }
export interface CustomHeader { header_key: string; header_value: string }

export interface ProtocolRoutingConfig {
    protocol_routing_enabled: boolean;
    mode: RoutingMode;
    protocol_fallback_enabled: boolean;
    protocol_learning_read_enabled: boolean;
    protocol_learning_write_enabled: boolean;
    protocol_conversion_enabled: boolean;
    adaptive_group_allowlist: number[];
    ranking_signal_order: string[];
    legacy_site_route_learning_enabled: boolean;
}

export interface ProtocolProfilePolicy {
    protocol: Protocol;
    enabled: boolean;
    base_urls: BaseUrl[];
    custom_headers: CustomHeader[];
    param_override?: string;
}

export interface ModelProtocolOverridePolicy {
    channel_key_id: number;
    upstream_model: string;
    mode: PolicyMode;
    preferred_protocols: Protocol[];
    enabled: boolean;
}

export interface ChannelProtocolPolicy {
    channel_id: number;
    profiles: ProtocolProfilePolicy[];
    overrides: ModelProtocolOverridePolicy[];
}

export interface ScopedProtocolPolicy {
    mode: PolicyMode;
    preferred_protocols: Protocol[];
}

export interface ProtocolPolicyState {
    active_revision: number;
    schema_version: number;
    config: ProtocolRoutingConfig;
    channels: ChannelProtocolPolicy[];
    groups: Array<ScopedProtocolPolicy & { group_id: number }>;
    group_presets: Array<ScopedProtocolPolicy & { group_preset_id: number }>;
}

export interface ProtocolRoutingConfigUpdate extends Partial<ProtocolRoutingConfig> {
    expected_revision: number;
}

export interface ChannelPolicyResponse {
    active_revision: number;
    policy: ChannelProtocolPolicy;
}

const policyKey = ['protocol-routing', 'policy'] as const;

export function useProtocolPolicy() {
    return useQuery({
        queryKey: policyKey,
        queryFn: () => apiClient.get<ProtocolPolicyState>('/api/v1/protocol-routing/policy'),
        staleTime: 10_000,
    });
}

function usePolicyMutation<T>(mutationFn: (data: T) => Promise<ProtocolPolicyState>) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn,
        onSuccess: (state) => queryClient.setQueryData(policyKey, state),
    });
}

export function useUpdateProtocolConfig() {
    return usePolicyMutation<ProtocolRoutingConfigUpdate>((data) =>
        apiClient.put('/api/v1/protocol-routing/config', data));
}

export function useChannelProtocolPolicy(channelId: number, enabled = true) {
    return useQuery({
        queryKey: ['protocol-routing', 'channel', channelId],
        queryFn: () => apiClient.get<ChannelPolicyResponse>(`/api/v1/protocol-routing/channels/${channelId}`),
        enabled,
    });
}

export function useReplaceChannelProtocolPolicy(channelId: number) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: { expected_revision: number; profiles: ProtocolProfilePolicy[]; overrides: ModelProtocolOverridePolicy[] }) =>
            apiClient.put<ProtocolPolicyState>(`/api/v1/protocol-routing/channels/${channelId}`, data),
        onSuccess: (state) => {
            queryClient.setQueryData(policyKey, state);
            queryClient.invalidateQueries({ queryKey: ['protocol-routing', 'channel', channelId] });
        },
    });
}

export function useUpdateScopedProtocolPolicy(kind: 'groups' | 'group-presets', id: number) {
    return usePolicyMutation<{ expected_revision: number; mode: PolicyMode; preferred_protocols: Protocol[] }>((data) =>
        apiClient.put(`/api/v1/protocol-routing/${kind}/${id}`, data));
}
