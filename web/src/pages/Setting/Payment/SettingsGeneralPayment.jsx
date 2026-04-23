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
  Col,
  Form,
  InputNumber,
  Row,
  Select,
  Space,
  Spin,
  Typography,
} from '@douyinfe/semi-ui';
import {
  API,
  getQuotaPerUnit,
  removeTrailingSlash,
  showError,
  showSuccess,
  verifyJSON,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

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

export default function SettingsGeneralPayment(props) {
  const { t } = useTranslation();
  const sectionTitle = props.hideSectionTitle ? undefined : t('通用设置');
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    ServerAddress: '',
    CustomCallbackAddress: '',
    TopupGroupRatio: '',
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
        ServerAddress: props.options.ServerAddress || '',
        CustomCallbackAddress: props.options.CustomCallbackAddress || '',
        TopupGroupRatio: props.options.TopupGroupRatio || '',
        PayMethods: props.options.PayMethods || '',
        AmountOptions: props.options.AmountOptions || '',
        AmountDiscount: props.options.AmountDiscount || '',
        AutoSwitchGroupEnabled:
          props.options.AutoSwitchGroupEnabled !== undefined
            ? props.options.AutoSwitchGroupEnabled
            : false,
        AutoSwitchGroupOnlyNewTopups:
          props.options.AutoSwitchGroupOnlyNewTopups !== undefined
            ? props.options.AutoSwitchGroupOnlyNewTopups
            : false,
        AutoSwitchGroupBaseGroup:
          props.options.AutoSwitchGroupBaseGroup || 'default',
      };

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
          showError(res.data?.message || t('分组加载失败，请刷新后重试'));
        }
      })
      .catch(() => {
        setGroupOptions([]);
        showError(t('分组加载失败，请刷新后重试'));
      })
      .finally(() => setGroupLoading(false));
  }, [t]);

  const handleFormChange = (values) => {
    setInputs((prev) =>
      normalizeAutoSwitchGroupInputs({
        ...prev,
        ...values,
      }),
    );
  };

  const submitGeneralSettings = async () => {
    if (
      originInputs.TopupGroupRatio !== inputs.TopupGroupRatio &&
      !verifyJSON(inputs.TopupGroupRatio)
    ) {
      showError(t('充值分组倍率不是合法的 JSON 字符串'));
      return;
    }

    if (
      originInputs.PayMethods !== inputs.PayMethods &&
      !verifyJSON(inputs.PayMethods)
    ) {
      showError(t('充值方式设置不是合法的 JSON 字符串'));
      return;
    }

    if (
      originInputs.AmountOptions !== inputs.AmountOptions &&
      inputs.AmountOptions.trim() !== '' &&
      !verifyJSON(inputs.AmountOptions)
    ) {
      showError(t('自定义充值数量选项不是合法的 JSON 数组'));
      return;
    }

    if (
      originInputs.AmountDiscount !== inputs.AmountDiscount &&
      inputs.AmountDiscount.trim() !== '' &&
      !verifyJSON(inputs.AmountDiscount)
    ) {
      showError(t('充值金额折扣配置不是合法的 JSON 对象'));
      return;
    }

    const normalizedInputs = normalizeAutoSwitchGroupInputs(inputs);
    const autoSwitchGroupRulesPayload = buildAutoSwitchGroupRulesPayload();
    if (autoSwitchGroupRulesPayload === null) {
      return;
    }

    const autoSwitchGroupRulesChanged =
      originAutoSwitchGroupRules !==
      serializeRuleRowsForCompare(autoSwitchGroupRules);
    const autoSwitchGroupEnabledChanged =
      originInputs.AutoSwitchGroupEnabled !==
      normalizedInputs.AutoSwitchGroupEnabled;
    const autoSwitchGroupOnlyNewTopupsChanged =
      originInputs.AutoSwitchGroupOnlyNewTopups !==
      normalizedInputs.AutoSwitchGroupOnlyNewTopups;
    const autoSwitchGroupBaseGroupChanged =
      originInputs.AutoSwitchGroupBaseGroup !==
      normalizedInputs.AutoSwitchGroupBaseGroup;
    const autoSwitchGroupUpdateNeeded =
      autoSwitchGroupRulesChanged ||
      autoSwitchGroupOnlyNewTopupsChanged ||
      autoSwitchGroupEnabledChanged ||
      autoSwitchGroupBaseGroupChanged;

    setLoading(true);
    try {
      const options = [
        {
          key: 'ServerAddress',
          value: removeTrailingSlash(normalizedInputs.ServerAddress),
        },
      ];

      if (normalizedInputs.CustomCallbackAddress !== '') {
        options.push({
          key: 'CustomCallbackAddress',
          value: removeTrailingSlash(normalizedInputs.CustomCallbackAddress),
        });
      }
      if (originInputs.TopupGroupRatio !== normalizedInputs.TopupGroupRatio) {
        options.push({
          key: 'TopupGroupRatio',
          value: normalizedInputs.TopupGroupRatio,
        });
      }
      if (originInputs.PayMethods !== normalizedInputs.PayMethods) {
        options.push({ key: 'PayMethods', value: normalizedInputs.PayMethods });
      }
      if (originInputs.AmountOptions !== normalizedInputs.AmountOptions) {
        options.push({
          key: 'payment_setting.amount_options',
          value: normalizedInputs.AmountOptions,
        });
      }
      if (originInputs.AmountDiscount !== normalizedInputs.AmountDiscount) {
        options.push({
          key: 'payment_setting.amount_discount',
          value: normalizedInputs.AmountDiscount,
        });
      }

      const results = await Promise.all(
        options.map((option) =>
          API.put('/api/option/', {
            key: option.key,
            value: option.value,
          }),
        ),
      );
      if (autoSwitchGroupUpdateNeeded) {
        const autoSwitchRes = await API.put(
          '/api/option/payment_auto_switch_group',
          {
            enabled: normalizedInputs.AutoSwitchGroupEnabled,
            only_new_topups: normalizedInputs.AutoSwitchGroupOnlyNewTopups,
            base_group: normalizedInputs.AutoSwitchGroupBaseGroup,
            rules: autoSwitchGroupRulesPayload,
          },
        );
        results.push(autoSwitchRes);
      }

      const errorResults = results.filter((res) => !res.data.success);
      if (errorResults.length === 0) {
        showSuccess(t('更新成功'));
        setOriginInputs({ ...normalizedInputs });
        setOriginAutoSwitchGroupRules(
          serializeRuleRowsForCompare(autoSwitchGroupRules),
        );
        await (props.refresh && props.refresh());
      } else {
        errorResults.forEach((res) => {
          showError(res.data.message);
        });
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
        initValues={inputs}
        onValueChange={handleFormChange}
        getFormApi={(api) => (formApiRef.current = api)}
      >
        <Form.Section text={sectionTitle}>
          <Form.Input
            field='ServerAddress'
            label={t('服务器地址')}
            placeholder={'https://yourdomain.com'}
            style={{ width: '100%' }}
            extraText={t(
              '该服务器地址将影响支付回调地址以及默认首页展示的地址，请确保正确配置',
            )}
          />
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.Input
                field='CustomCallbackAddress'
                label={t('回调地址')}
                placeholder={t('例如：https://yourdomain.com')}
                extraText={t(
                  '留空时默认使用服务器地址作为回调地址，填写后将覆盖默认值',
                )}
              />
            </Col>
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.TextArea
                field='TopupGroupRatio'
                label={t('充值分组倍率')}
                placeholder={t('为一个 JSON 文本，键为组名称，值为倍率')}
                autosize
              />
            </Col>
          </Row>
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
              <Form.TextArea
                field='PayMethods'
                label={t('充值方式设置')}
                placeholder={t('为一个 JSON 文本')}
                autosize
              />
            </Col>
            <Col xs={24} sm={24} md={12} lg={12} xl={12}>
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
          <Row style={{ marginTop: 16 }}>
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
          <Row
            gutter={{ xs: 8, sm: 16, md: 24, lg: 24, xl: 24, xxl: 24 }}
            style={{ marginTop: 16 }}
          >
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Switch
                field='AutoSwitchGroupEnabled'
                label={t('充值后自动切换分组')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Switch
                field='AutoSwitchGroupOnlyNewTopups'
                label={t('启用后仅统计新充值')}
                size='default'
                checkedText='｜'
                uncheckedText='〇'
                disabled={!inputs.AutoSwitchGroupEnabled}
              />
            </Col>
            <Col xs={24} sm={24} md={8} lg={8} xl={8}>
              <Form.Select
                field='AutoSwitchGroupBaseGroup'
                label={t('基础分组')}
                placeholder={t('请选择基础分组')}
                loading={groupLoading}
                optionList={(groupOptions || []).map((group) => ({
                  label: group,
                  value: group,
                }))}
              />
            </Col>
          </Row>
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
          <Button onClick={submitGeneralSettings} style={{ marginTop: 16 }}>
            {t('保存通用设置')}
          </Button>
        </Form.Section>
      </Form>
    </Spin>
  );
}
