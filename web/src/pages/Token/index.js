import React from 'react';
import TokensTable from '../../components/TokensTable';
import { Banner, Layout } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
const Token = () => {
  const { t } = useTranslation();
  return (
    <>
      <Layout>
        <Layout.Header>
        <Banner
          type='warning'
          description={t('令牌无法精确计算使用额度，只允许自用，请勿直接将令牌分享他人。')}
        />
      </Layout.Header>
      <Layout.Content>
        <TokensTable />
        </Layout.Content>
      </Layout>
    </>
  );
};

export default Token;
