import React, { useEffect, useState } from 'react';
import { useTokenKeys } from '../../components/fetchTokenKeys';
import {Banner, Layout} from '@douyinfe/semi-ui';
import { useParams } from 'react-router-dom';

// 通过 `/chat2link` 自动跳转对话页面"
// 客户只需要在主页设置超链接：<a href="https://{your_server_url}/chat2link">点击开始聊天</a>可直接跳转到设置的第一个聊天链接

const chat2page = () => {
  const { id } = useParams();
  const { keys, serverAddress, isLoading } = useTokenKeys(id);
  const [chatLink, setChatLink] = useState('');

  useEffect(() => {
    const link = localStorage.getItem('chat_link');
    setChatLink(link || ''); 
  }, []);

  const comLink = (key) => {
    if (!serverAddress || !key) return '';
    let finalLink = '';
    // 保留原chatLink生成逻辑
    if (chatLink) {
      finalLink = `${chatLink}/#/?settings={"key":"sk-${key}","url":"${encodeURIComponent(serverAddress)}"}`;
    } else {
      // 从新的chats中获取第一个id的链接，
      const chatsData = localStorage.getItem('chats');
      if (chatsData) {
        try {
          const parsedChats = JSON.parse(chatsData);
          if (parsedChats && typeof parsedChats === 'object') {
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
          console.error('获取chat链接失败:', error);
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
        {/* 统一new的chat模板， */}
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
