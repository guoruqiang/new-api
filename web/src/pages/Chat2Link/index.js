import React, { useEffect, useState } from 'react';
import { useTokenKeys } from '../../components/fetchTokenKeys';
import {Banner, Layout} from '@douyinfe/semi-ui';
import { useParams } from 'react-router-dom';

const chat2page = () => {
  const { id } = useParams();
  const { keys, serverAddress, isLoading } = useTokenKeys(id);
  const [chatLink, setChatLink] = useState('');

  useEffect(() => {
    const link = localStorage.getItem('chat_link');
    setChatLink(link || ''); // 如果不存在chat_link，设置为空字符串
  }, []);

  const comLink = (key) => {
    if (!serverAddress || !key) return '';

    let finalLink = '';

    // 优先使用chatLink生成原始链接
    if (chatLink) {
      finalLink = `${chatLink}/#/?settings={"key":"sk-${key}","url":"${encodeURIComponent(serverAddress)}"}`;
    } else {
      // 从chats中获取第一个id的第一个模板
      const chatLinks = localStorage.getItem('chats');
      if (chatLinks) {
        try {
          const parsedChats = JSON.parse(chatLinks);
          if (parsedChats && typeof parsedChats === 'object') {
            // 获取所有id，优先使用路由参数id，否则取第一个id
            const targetId = id || Object.keys(parsedChats)[0];
            if (targetId && parsedChats[targetId]) {
              const templates = parsedChats[targetId];
              const templateKeys = Object.keys(templates);
              if (templateKeys.length > 0) {
                const firstTemplate = templates[templateKeys[0]];
                finalLink = firstTemplate
                  .replace(/{address}/g, encodeURIComponent(serverAddress))
                  .replace(/{key}/g, 'sk-' + key);
              }
            }
          }
        } catch (error) {
          console.error('解析chats失败:', error);
        }
      }
    }

    return finalLink;
  };

  useEffect(() => {
    if (keys.length > 0) {
      const redirectLink = comLink(keys[0]);
      if (redirectLink) {
        window.location.href = redirectLink;
      }
    }
  }, [keys, chatLink, serverAddress, id]);

  return (
    <div>
      <Layout>
        <Layout.Header>
          <Banner
              description={"正在跳转......"}
              type={"warning"}
          />
        </Layout.Header>
      </Layout>
    </div>
  );
};

export default chat2page;
