import assert from 'node:assert/strict';
import test from 'node:test';
import {
    compressConfigForTier,
    compressConfigPayload,
    compressTierOf,
    nextCompressConfigForToggle,
    normalizeGroupCompressConfig,
} from '../src/components/modules/group/groupCompressConfig.ts';

// 该缺陷曾两次上线:分组压缩开关点不开(回传的 current.enabled 仍取自旧 value)。
// 这两个测试锁定「从未配置过 → 打开」这条完整路径,防止再次回归。
test('toggling on from an unconfigured group yields enabled:true', () => {
    // 分组从未配置压缩时,后端不下发 compress_config,组件推导出的 current.enabled 为 false。
    const current = normalizeGroupCompressConfig(undefined) ?? {
        enabled: false,
        lite: true,
        headroom: false,
        output_style: '',
    };

    assert.equal(nextCompressConfigForToggle(current, true).enabled, true);
});

test('toggling on then submitting sends compress_config with enabled:true', () => {
    const prev = normalizeGroupCompressConfig(undefined); // 后端未下发 → undefined
    const next = nextCompressConfigForToggle(
        { enabled: false, lite: true, headroom: false, output_style: '' },
        true,
    );

    assert.deepEqual(compressConfigPayload(prev, next), {
        enabled: true,
        lite: true,
        headroom: false,
        output_style: '',
    });
});

test('toggling off clears the config only when it was enabled before', () => {
    const enabled = { enabled: true, lite: true, headroom: false, output_style: '' };

    // 曾开启 → 显式发 enabled:false 覆盖后端配置
    assert.deepEqual(compressConfigPayload(enabled, undefined), {
        enabled: false,
        lite: false,
        headroom: false,
        output_style: '',
    });
    // 本就未开启 → 不携带该字段,避免空更新
    assert.equal(compressConfigPayload(undefined, undefined), undefined);
});

test('unchanged config is not resubmitted', () => {
    const cfg = { enabled: true, lite: true, headroom: true, output_style: '' };
    assert.equal(compressConfigPayload(cfg, { ...cfg }), undefined);
});

test('normalize defaults lite on and rejects unknown output styles', () => {
    assert.deepEqual(normalizeGroupCompressConfig({ enabled: true }), {
        enabled: true,
        lite: true,
        headroom: false,
        output_style: '',
    });
    // enabled 只认 true;字符串 'true' 不应被当作开启
    assert.equal(normalizeGroupCompressConfig({ enabled: 'true' }).enabled, false);
    assert.equal(normalizeGroupCompressConfig({ output_style: 'bogus' }).output_style, '');
    assert.equal(normalizeGroupCompressConfig(undefined), undefined);
});

test('tier round-trips through its engine combination', () => {
    for (const tier of ['low', 'medium', 'high']) {
        assert.equal(compressTierOf(compressConfigForTier(tier)), tier);
        assert.equal(compressConfigForTier(tier).enabled, true);
    }
    // 手动改动引擎后落到 custom
    assert.equal(compressTierOf({ enabled: true, lite: false, headroom: true, output_style: '' }), 'custom');
});
