'use client';

import { useTranslations } from 'next-intl';
import { Shrink } from 'lucide-react';
import { Switch } from '@/components/ui/switch';
import { SettingKey } from '@/api/endpoints/setting';
import { SettingCard, SettingRow, useSettingToggle } from './shared';

// 请求压缩全局急停开关。分组内的压缩配置(compress_config)需此开关开启才会生效;
// 关闭后立即对所有分组停手,是压缩能力的总开关。开关即时保存、失败回滚(见 useSettingToggle)。
export function SettingCompress() {
    const t = useTranslations('setting');
    const master = useSettingToggle(SettingKey.CompressMasterEnabled);

    return (
        <SettingCard icon={Shrink} title={t('compress.title')}>
            <SettingRow label={t('compress.enabled.label')} tooltip={t('compress.enabled.description')}>
                <Switch checked={master.enabled} onCheckedChange={master.toggle} />
            </SettingRow>
        </SettingCard>
    );
}
