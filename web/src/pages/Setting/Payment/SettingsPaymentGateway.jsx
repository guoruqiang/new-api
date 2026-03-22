/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useState, useRef } from 'react';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import {
  Banner,
  Button,
  Form,
  Row,
  Col,
  Typography,
  Spin,
  InputNumber,
  Select,
  Space,
  Switch,
} from '@douyinfe/semi-ui';
const { Text } = Typography;
import {
  API,
  compareObjects,
  getQuotaPerUnit,
  removeTrailingSlash,
  showError,
  showSuccess,
  showWarning,
  verifyJSON,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

const createRuleId = () =>
  `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;

const createRuleRow = (rule = {}) => ({
  id: createRuleId(),
  threshold:
    rule.threshold === undefined || rule.threshold === null
      ? ''
      : rule.threshold,
  group: String(rule.group || ''),
});

const serializeRuleRowsForCompare = (rules = []) =>
  JSON.stringify(
    rules.map((rule) => {
      const threshold = Number(rule.threshold);
      return {
        threshold: Number.isFinite(threshold) ? threshold : null,
        group: String(rule.group || '').trim(),
      };
    }),
  );

const normalizeAutoSwitchGroupInputs = (values = {}) => ({
  ...values,
  AutoSwitchGroupEnabled: Boolean(values.AutoSwitchGroupEnabled),
  AutoSwitchGroupOnlyNewTopups:
    Boolean(values.AutoSwitchGroupEnabled) &&
    Boolean(values.AutoSwitchGroupOnlyNewTopups),
  AutoSwitchGroupBaseGroup: String(
    values.AutoSwitchGroupBaseGroup || 'default',
  ),
});

export default function SettingsPaymentGateway(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    PayAddress: '',
    EpayId: '',
    EpayKey: '',
    Price: 7.3,
    MinTopUp: 1,
    TopupGroupRatio: '',
    CustomCallbackAddress: '',
    PayMethods: '',
    AmountOptions: '',
    AmountDiscount: '',
    AutoSwitchGroupEnabled: false,
    AutoSwitchGroupOnlyNewTopups: false,
    AutoSwitchGroupBaseGroup: 'default',
  });
  const [originInputs, setOriginInputs] = useState({});
  const [groupOptions, setGroupOptions] = useState([]);
  const [groupLoading, setGroupLoading] = useState(false);
  const [autoSwitchGroupRules, setAutoSwitchGroupRules] = useState([]);
  const [originAutoSwitchGroupRules, setOriginAutoSwitchGroupRules] =
    useState('[]');
  const formApiRef = useRef(null);

  const getDisplayConfig = () => {
    let type = props.options?.['general_setting.quota_display_type'];
    if (!type && props.options?.DisplayInCurrencyEnabled !== undefined) {
      type = props.options.DisplayInCurrencyEnabled ? 'USD' : 'TOKENS';
    }
    if (!type) {
      type = 'USD';
    }

    const usdExchangeRate = Number(props.options?.USDExchangeRate || 1);
    const customRate = Number(
      props.options?.['general_setting.custom_currency_exchange_rate'] || 1,
    );
    const customSymbol =
      props.options?.['general_setting.custom_currency_symbol'] || 'CUSTOM';
    const quotaPerUnit = Number(
      props.options?.QuotaPerUnit || getQuotaPerUnit(),
    );

    if (type === 'TOKENS') {
      return {
        type,
        multiplier:
          Number.isFinite(quotaPerUnit) && quotaPerUnit > 0 ? quotaPerUnit : 1,
        unitLabel: 'TOKENS',
        precision: 0,
      };
    }

    if (type === 'CNY') {
      return {
        type,
        multiplier:
          Number.isFinite(usdExchangeRate) && usdExchangeRate > 0
            ? usdExchangeRate
            : 1,
        unitLabel: 'CNY',
        precision: 6,
      };
    }

    if (type === 'CUSTOM') {
      return {
        type,
        multiplier:
          Number.isFinite(customRate) && customRate > 0 ? customRate : 1,
        unitLabel: customSymbol || 'CUSTOM',
        precision: 6,
      };
    }

    return {
      type: 'USD',
      multiplier: 1,
      unitLabel: 'USD',
      precision: 6,
    };
  };

  const convertUsdToDisplayThreshold = (usdValue) => {
    const numeric = Number(usdValue || 0);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return '';
    }
    const displayConfig = getDisplayConfig();
    const converted = numeric * displayConfig.multiplier;
    if (displayConfig.type === 'TOKENS') {
      return Math.round(converted);
    }
    return Number(converted.toFixed(6));
  };

  const convertDisplayThresholdToUsd = (displayValue) => {
    const numeric = Number(displayValue);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return 0;
    }
    const displayConfig = getDisplayConfig();
    return Number((numeric / displayConfig.multiplier).toFixed(8));
  };

  const buildAutoSwitchGroupRulesPayload = () => {
    const effectiveRules = autoSwitchGroupRules
      .map((rule) => ({
        threshold: rule.threshold,
        group: String(rule.group || '').trim(),
      }))
      .filter((rule) => rule.threshold !== '' || rule.group !== '');

    if (effectiveRules.length === 0) {
      if (inputs.AutoSwitchGroupEnabled) {
        showError(t('启用充值后自动切换分组前，请至少配置一条合法规则'));
        return null;
      }
      return [];
    }

    const payload = [];
    for (let index = 0; index < effectiveRules.length; index += 1) {
      const rule = effectiveRules[index];
      const threshold = Number(rule.threshold);
      if (!Number.isFinite(threshold) || threshold <= 0) {
        showError(
          t('第 {{index}} 条充值自动切换分组规则的阈值必须大于 0', {
            index: index + 1,
          }),
        );
        return null;
      }
      if (!rule.group) {
        showError(
          t('第 {{index}} 条充值自动切换分组规则请选择分组', {
            index: index + 1,
          }),
        );
        return null;
      }

      payload.push({
        threshold_usd: convertDisplayThresholdToUsd(threshold),
        group: rule.group,
      });
    }

    payload.sort((a, b) => a.threshold_usd - b.threshold_usd);
    return payload;
  };

  const updateAutoSwitchGroupRule = (ruleId, patch) => {
    setAutoSwitchGroupRules((prev) =>
      prev.map((rule) => (rule.id === ruleId ? { ...rule, ...patch } : rule)),
    );
  };

  const addAutoSwitchGroupRule = () => {
    setAutoSwitchGroupRules((prev) => [...prev, createRuleRow()]);
  };

  const removeAutoSwitchGroupRule = (ruleId) => {
    setAutoSwitchGroupRules((prev) =>
      prev.filter((rule) => rule.id !== ruleId),
    );
  };

  useEffect(() => {
    if (props.options && formApiRef.current) {
      const currentInputs = {
        PayAddress: props.options.PayAddress || '',
        EpayId: props.options.EpayId || '',
        EpayKey: props.options.EpayKey || '',
        Price:
          props.options.Price !== undefined
            ? parseFloat(props.options.Price)
            : 7.3,
        MinTopUp:
          props.options.MinTopUp !== undefined
            ? parseFloat(props.options.MinTopUp)
            : 1,
        TopupGroupRatio: props.options.TopupGroupRatio || '',
        CustomCallbackAddress: props.options.CustomCallbackAddress || '',
        PayMethods: props.options.PayMethods || '',
        AmountOptions: props.options.AmountOptions || '',
        AmountDiscount: props.options.AmountDiscount || '',
        AutoSwitchGroupEnabled: props.options.AutoSwitchGroupEnabled || false,
        AutoSwitchGroupOnlyNewTopups:
          props.options.AutoSwitchGroupOnlyNewTopups || false,
        AutoSwitchGroupBaseGroup:
          props.options.AutoSwitchGroupBaseGroup || 'default',
      };

      // 美化 JSON 展示
      try {
        if (currentInputs.AmountOptions) {
          currentInputs.AmountOptions = JSON.stringify(
            JSON.parse(currentInputs.AmountOptions),
            null,
            2,
          );
        }
      } catch {}
      try {
        if (currentInputs.AmountDiscount) {
          currentInputs.AmountDiscount = JSON.stringify(
            JSON.parse(currentInputs.AmountDiscount),
            null,
            2,
          );
        }
      } catch {}

      const nextRules = Array.isArray(props.options.AutoSwitchGroupRules)
        ? props.options.AutoSwitchGroupRules.map((rule) =>
            createRuleRow({
              threshold: convertUsdToDisplayThreshold(rule.threshold_usd),
              group: rule.group || '',
            }),
          )
        : [];

      const normalizedCurrentInputs =
        normalizeAutoSwitchGroupInputs(currentInputs);
      setInputs(normalizedCurrentInputs);
      setOriginInputs({ ...normalizedCurrentInputs });
      setAutoSwitchGroupRules(nextRules);
      setOriginAutoSwitchGroupRules(serializeRuleRowsForCompare(nextRules));
      formApiRef.current.setValues(normalizedCurrentInputs);
    }
  }, [props.options]);

  useEffect(() => {
    setGroupLoading(true);
    API.get('/api/group')
      .then((res) => {
        if (res.data?.success) {
          setGroupOptions(res.data?.data || []);
        } else {
          setGroupOptions([]);
        }
      })
      .catch(() => setGroupOptions([]))
      .finally(() => setGroupLoading(false));
  }, []);

  const handleFormChange = (values) => {
    const normalizedValues = normalizeAutoSwitchGroupInputs({
      ...values,
      AutoSwitchGroupEnabled: inputs.AutoSwitchGroupEnabled,
      AutoSwitchGroupOnlyNewTopups: inputs.AutoSwitchGroupOnlyNewTopups,
      AutoSwitchGroupBaseGroup: inputs.AutoSwitchGroupBaseGroup,
    });
    setInputs(normalizedValues);
  };

  const handleAutoSwitchGroupEnabledChange = (checked) => {
    setInputs((prev) =>
      normalizeAutoSwitchGroupInputs({
        ...prev,
        AutoSwitchGroupEnabled: checked,
        AutoSwitchGroupOnlyNewTopups: checked
          ? prev.AutoSwitchGroupOnlyNewTopups
          : false,
      }),
    );
  };

  const handleAutoSwitchGroupOnlyNewTopupsChange = (checked) => {
    setInputs((prev) =>
      normalizeAutoSwitchGroupInputs({
        ...prev,
        AutoSwitchGroupEnabled: true,
        AutoSwitchGroupOnlyNewTopups: checked,
      }),
    );
  };

  const submitPayAddress = async () => {
    if (props.options.ServerAddress === '') {
      showError(t('请先填写服务器地址'));
      return;
    }

    if (originInputs['TopupGroupRatio'] !== inputs.TopupGroupRatio) {
      if (!verifyJSON(inputs.TopupGroupRatio)) {
        showError(t('充值分组倍率不是合法的 JSON 字符串'));
        return;
      }
    }

    if (originInputs['PayMethods'] !== inputs.PayMethods) {
      if (!verifyJSON(inputs.PayMethods)) {
        showError(t('充值方式设置不是合法的 JSON 字符串'));
        return;
      }
    }

    if (
      originInputs['AmountOptions'] !== inputs.AmountOptions &&
      inputs.AmountOptions.trim() !== ''
    ) {
      if (!verifyJSON(inputs.AmountOptions)) {
        showError(t('自定义充值数量选项不是合法的 JSON 数组'));
        return;
      }
    }

    if (
      originInputs['AmountDiscount'] !== inputs.AmountDiscount &&
      inputs.AmountDiscount.trim() !== ''
    ) {
      if (!verifyJSON(inputs.AmountDiscount)) {
        showError(t('充值金额折扣配置不是合法的 JSON 对象'));
        return;
      }
    }

    const autoSwitchGroupRulesPayload = buildAutoSwitchGroupRulesPayload();
    if (autoSwitchGroupRulesPayload === null) {
      return;
    }

    try {
      const normalizedInputs = {
        ...inputs,
        PayAddress: removeTrailingSlash(inputs.PayAddress),
      };
      const comparedInputs = {
        ...normalizedInputs,
        EpayKey:
          normalizedInputs.EpayKey === undefined
            ? ''
            : normalizedInputs.EpayKey,
      };
      const updateArray = compareObjects(originInputs, comparedInputs);
      const options = updateArray
        .filter(
          (item) =>
            item.key !== 'AutoSwitchGroupRules' &&
            item.key !== 'AutoSwitchGroupEnabled' &&
            item.key !== 'AutoSwitchGroupOnlyNewTopups' &&
            item.key !== 'AutoSwitchGroupBaseGroup',
        )
        .map((item) => ({
          key:
            item.key === 'AmountOptions'
              ? 'payment_setting.amount_options'
              : item.key === 'AmountDiscount'
                ? 'payment_setting.amount_discount'
                : item.key,
          value:
            typeof comparedInputs[item.key] === 'boolean'
              ? comparedInputs[item.key].toString()
              : comparedInputs[item.key],
        }));

      const autoSwitchGroupRulesChanged =
        originAutoSwitchGroupRules !==
        serializeRuleRowsForCompare(autoSwitchGroupRules);
      const autoSwitchGroupEnabledChanged =
        originInputs['AutoSwitchGroupEnabled'] !==
        inputs.AutoSwitchGroupEnabled;
      const autoSwitchGroupOnlyNewTopupsChanged =
        originInputs['AutoSwitchGroupOnlyNewTopups'] !==
        inputs.AutoSwitchGroupOnlyNewTopups;
      const autoSwitchGroupBaseGroupChanged =
        originInputs['AutoSwitchGroupBaseGroup'] !==
        inputs.AutoSwitchGroupBaseGroup;

      if (
        autoSwitchGroupRulesChanged ||
        autoSwitchGroupOnlyNewTopupsChanged ||
        autoSwitchGroupEnabledChanged ||
        autoSwitchGroupBaseGroupChanged
      ) {
        const autoSwitchOptions = [];
        if (
          normalizedInputs.AutoSwitchGroupEnabled ||
          autoSwitchGroupBaseGroupChanged
        ) {
          autoSwitchOptions.push({
            key: 'payment_setting.auto_switch_group_base_group',
            value: normalizedInputs.AutoSwitchGroupBaseGroup,
          });
        }
        if (normalizedInputs.AutoSwitchGroupEnabled) {
          autoSwitchOptions.push({
            key: 'payment_setting.auto_switch_group_rules',
            value: JSON.stringify(autoSwitchGroupRulesPayload),
          });
          autoSwitchOptions.push({
            key: 'payment_setting.auto_switch_group_only_new_topups',
            value: normalizedInputs.AutoSwitchGroupOnlyNewTopups.toString(),
          });
          autoSwitchOptions.push({
            key: 'payment_setting.auto_switch_group_enabled',
            value: normalizedInputs.AutoSwitchGroupEnabled.toString(),
          });
        } else {
          if (autoSwitchGroupEnabledChanged) {
            autoSwitchOptions.push({
              key: 'payment_setting.auto_switch_group_enabled',
              value: normalizedInputs.AutoSwitchGroupEnabled.toString(),
            });
          }
          if (autoSwitchGroupOnlyNewTopupsChanged) {
            autoSwitchOptions.push({
              key: 'payment_setting.auto_switch_group_only_new_topups',
              value: normalizedInputs.AutoSwitchGroupOnlyNewTopups.toString(),
            });
          }
          if (autoSwitchGroupRulesChanged) {
            autoSwitchOptions.push({
              key: 'payment_setting.auto_switch_group_rules',
              value: JSON.stringify(autoSwitchGroupRulesPayload),
            });
          }
        }
        options.push(...autoSwitchOptions);
      }

      if (!options.length) {
        showWarning(t('你似乎并没有修改什么'));
        return;
      }

      setLoading(true);
      const results = [];
      for (const opt of options) {
        const res = await API.put('/api/option/', {
          key: opt.key,
          value: opt.value,
        });
        results.push(res);
      }

      const errorResults = results.filter((res) => !res.data.success);
      if (errorResults.length > 0) {
        errorResults.forEach((res) => {
          showError(res.data.message);
        });
      } else {
        showSuccess(t('更新成功'));
        setOriginInputs({ ...normalizedInputs });
        setOriginAutoSwitchGroupRules(
          serializeRuleRowsForCompare(autoSwitchGroupRules),
        );
        await (props.refresh && props.refresh());
      }
    } catch (error) {
      showError(t('更新失败'));
    }
    setLoading(false);
  };

  const displayConfig = getDisplayConfig();

  return (
    <Spin spinning={loading}>
      <Form
        values={inputs}
        onValueChange={handleFormChange}
        getFormApi={(api) => (formApiRef.current = api)}
      >
        <Form.Section text={t('支付设置')}>
          <Text>
            {t(
              '（当前仅支持易支付接口，默认使用上方服务器地址作为回调地址！）',
            )}
          </Text>
          <Row gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Input
                field='PayAddress'
                label={t('支付地址')}
                placeholder={t('例如：https://yourdomain.com')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Input
                field='EpayId'
                label={t('易支付商户ID')}
                placeholder={t('例如：0001')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Input
                field='EpayKey'
                label={t('易支付商户密钥')}
                placeholder={t('敏感信息不会发送到前端显示')}
                type='password'
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Input
                field='CustomCallbackAddress'
                label={t('回调地址')}
                placeholder={t('例如：https://yourdomain.com')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.InputNumber
                field='Price'
                precision={2}
                label={t('充值价格（x元/美金）')}
                placeholder={t('例如：7，就是7元/美金')}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.InputNumber
                field='MinTopUp'
                label={t('最低充值美元数量')}
                placeholder={t('例如：2，就是最低充值2$')}
              />
            </Col>
          </Row>
          <Form.TextArea
            field='TopupGroupRatio'
            label={t('充值分组倍率')}
            placeholder={t('为一个 JSON 文本，键为组名称，值为倍率')}
            autosize
          />
          <Form.TextArea
            field='PayMethods'
            label={t('充值方式设置')}
            placeholder={t('为一个 JSON 文本')}
            autosize
          />

          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col span={24}>
              <Form.TextArea
                field='AmountOptions'
                label={t('自定义充值数量选项')}
                placeholder={t(
                  '为一个 JSON 数组，例如：[10, 20, 50, 100, 200, 500]',
                )}
                autosize
                extraText={t(
                  '设置用户可选择的充值数量选项，例如：[10, 20, 50, 100, 200, 500]',
                )}
              />
            </Col>
          </Row>

          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col span={24}>
              <Form.TextArea
                field='AmountDiscount'
                label={t('充值金额折扣配置')}
                placeholder={t(
                  '为一个 JSON 对象，例如：{"100": 0.95, "200": 0.9, "500": 0.85}',
                )}
                autosize
                extraText={t(
                  '设置不同充值金额对应的折扣，键为充值金额，值为折扣率，例如：{"100": 0.95, "200": 0.9, "500": 0.85}',
                )}
              />
            </Col>
          </Row>

          <div
            style={{
              marginTop: 16,
              display: 'flex',
              alignItems: 'center',
              gap: 24,
              flexWrap: 'wrap',
            }}
          >
            <div>
              <Text>{t('充值后自动切换分组')}</Text>
              <div style={{ marginTop: 6 }}>
                <Switch
                  checked={inputs.AutoSwitchGroupEnabled}
                  size='large'
                  onChange={handleAutoSwitchGroupEnabledChange}
                />
              </div>
            </div>
            {inputs.AutoSwitchGroupEnabled && (
              <div>
                <Text>{t('启用后仅统计新充值')}</Text>
                <div style={{ marginTop: 6 }}>
                  <Switch
                    checked={inputs.AutoSwitchGroupOnlyNewTopups}
                    size='large'
                    onChange={handleAutoSwitchGroupOnlyNewTopupsChange}
                  />
                </div>
              </div>
            )}
            <div style={{ minWidth: 220 }}>
              <Text>{t('基础分组')}</Text>
              <Select
                style={{ width: '100%', marginTop: 6 }}
                value={inputs.AutoSwitchGroupBaseGroup || 'default'}
                loading={groupLoading}
                placeholder={t('请选择基础分组')}
                onChange={(value) =>
                  setInputs((prev) =>
                    normalizeAutoSwitchGroupInputs({
                      ...prev,
                      AutoSwitchGroupBaseGroup: value || 'default',
                    }),
                  )
                }
              >
                {(groupOptions || []).map((group) => (
                  <Select.Option key={group} value={group}>
                    {group}
                  </Select.Option>
                ))}
              </Select>
            </div>
          </div>
          <div style={{ marginTop: 12 }}>
            <Banner
              type='info'
              description={t(
                '按累计成功的普通充值金额命中规则后切换分组；开启“仅统计新充值”后会从当前时刻重新累计，不回溯历史成功充值。',
              )}
            />
            <Banner
              type='warning'
              description={`${t(
                '仅对普通充值生效，不影响 subscription 套餐购买时的 upgrade_group 逻辑。',
              )} ${t('当前按 {{unit}} 编辑阈值，保存后统一换算为 USD。', {
                unit: displayConfig.unitLabel,
              })}`}
              style={{ marginTop: 8 }}
            />
          </div>
          <Space
            vertical
            align='start'
            spacing={12}
            style={{ width: '100%', marginTop: 12 }}
          >
            {autoSwitchGroupRules.length === 0 ? (
              <Text type='tertiary'>{t('暂无规则，请点击下方按钮新增')}</Text>
            ) : (
              autoSwitchGroupRules.map((rule) => (
                <div key={rule.id} style={{ width: '100%' }}>
                  <div
                    style={{
                      display: 'flex',
                      gap: 12,
                      alignItems: 'flex-end',
                      flexWrap: 'wrap',
                    }}
                  >
                    <div style={{ flex: 1, minWidth: 180 }}>
                      <Text>{`${t('阈值')} (${displayConfig.unitLabel})`}</Text>
                      <InputNumber
                        style={{ width: '100%', marginTop: 6 }}
                        min={displayConfig.type === 'TOKENS' ? 1 : 0.000001}
                        precision={displayConfig.precision}
                        value={
                          rule.threshold === '' ? undefined : rule.threshold
                        }
                        placeholder={t('请输入阈值')}
                        onChange={(value) =>
                          updateAutoSwitchGroupRule(rule.id, {
                            threshold: value ?? '',
                          })
                        }
                      />
                    </div>
                    <div style={{ flex: 1, minWidth: 180 }}>
                      <Text>{t('分组')}</Text>
                      <Select
                        style={{ width: '100%', marginTop: 6 }}
                        value={rule.group || undefined}
                        loading={groupLoading}
                        showClear
                        placeholder={t('请选择分组')}
                        onChange={(value) =>
                          updateAutoSwitchGroupRule(rule.id, {
                            group: value || '',
                          })
                        }
                      >
                        {(groupOptions || []).map((group) => (
                          <Select.Option key={group} value={group}>
                            {group}
                          </Select.Option>
                        ))}
                      </Select>
                    </div>
                    <Button
                      icon={<IconDelete />}
                      theme='borderless'
                      type='danger'
                      onClick={() => removeAutoSwitchGroupRule(rule.id)}
                    >
                      {t('删除规则')}
                    </Button>
                  </div>
                </div>
              ))
            )}

            <Button icon={<IconPlus />} onClick={addAutoSwitchGroupRule}>
              {t('新增规则')}
            </Button>
          </Space>

          <Button onClick={submitPayAddress} style={{ marginTop: 16 }}>
            {t('更新支付设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}
