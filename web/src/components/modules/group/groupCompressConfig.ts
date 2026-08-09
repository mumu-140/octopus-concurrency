/**
 * 分组级请求压缩配置的纯逻辑。与后端 model.GroupCompressConfig 字段对应。
 *
 * 独立成模块(不依赖 react-query / apiClient / 路径别名)以便 web/tests 直接导入验证:
 * 开关与提交 diff 曾出现过「全局已开、分组开关点不开」的缺陷,这里的函数是该行为的唯一来源。
 *
 * enabled 为分组压缩总开关(还需全局 compress_master_enabled 开启才生效)。
 * lite: 空白折叠 + system 去重 + tool 结果截断; headroom: 同构 JSON 数组转列式表;
 * output_style: 输出风格注入("" | "terse-prose" | "terse-cjk")。
 */
export type CompressOutputStyle = '' | 'terse-prose' | 'terse-cjk';

export interface GroupCompressConfig {
    enabled: boolean;
    lite: boolean;
    headroom: boolean;
    output_style: CompressOutputStyle;
}

export type CompressTier = 'low' | 'medium' | 'high' | 'custom';

export const COMPRESS_OUTPUT_STYLES: CompressOutputStyle[] = ['', 'terse-prose', 'terse-cjk'];

function normalizeOutputStyle(value: unknown): CompressOutputStyle {
    return (COMPRESS_OUTPUT_STYLES as string[]).includes(value as string)
        ? (value as CompressOutputStyle)
        : '';
}

export function normalizeGroupCompressConfig(value: unknown): GroupCompressConfig | undefined {
    if (!value || typeof value !== 'object') return undefined;
    const v = value as Record<string, unknown>;
    return {
        enabled: v.enabled === true,
        lite: v.lite !== false,        // 默认开启最低档
        headroom: v.headroom === true,
        output_style: normalizeOutputStyle(v.output_style),
    };
}

// 预设档位 → 引擎组合。low 为最低档(默认)。
export function compressConfigForTier(tier: Exclude<CompressTier, 'custom'>): GroupCompressConfig {
    switch (tier) {
        case 'low':
            return { enabled: true, lite: true, headroom: false, output_style: '' };
        case 'medium':
            return { enabled: true, lite: true, headroom: true, output_style: '' };
        case 'high':
            return { enabled: true, lite: true, headroom: true, output_style: 'terse-prose' };
    }
}

// 由引擎组合反推档位;无法匹配预设档时返回 'custom'。
export function compressTierOf(cfg: GroupCompressConfig): CompressTier {
    if (cfg.lite && !cfg.headroom && cfg.output_style === '') return 'low';
    if (cfg.lite && cfg.headroom && cfg.output_style === '') return 'medium';
    if (cfg.lite && cfg.headroom && cfg.output_style === 'terse-prose') return 'high';
    return 'custom';
}

/**
 * 分组压缩开关的下一个状态。
 *
 * 开启必须显式写 enabled:true: 调用方的 current 由旧 value 推导而来,开启时其 enabled 仍为
 * false,直接回传会把开关钉死在关闭状态(既有缺陷)。关闭返回 undefined,提交时清除压缩配置。
 */
export function nextCompressConfigForToggle(
    current: GroupCompressConfig,
    checked: boolean,
): GroupCompressConfig | undefined {
    return checked ? { ...current, enabled: true } : undefined;
}

/**
 * 提交时的 compress_config 增量:整体替换语义。
 * 返回 undefined 表示本次提交不携带该字段。
 * 关闭(next === undefined)且此前曾开启过时,显式发 enabled:false 以清除后端配置;
 * 若此前本就未开启则无需发送,避免产生空更新。
 */
export function compressConfigPayload(
    prev: GroupCompressConfig | undefined,
    next: GroupCompressConfig | undefined,
): GroupCompressConfig | undefined {
    if (next === undefined) {
        return prev?.enabled ? { enabled: false, lite: false, headroom: false, output_style: '' } : undefined;
    }
    return JSON.stringify(next) !== JSON.stringify(prev) ? next : undefined;
}
