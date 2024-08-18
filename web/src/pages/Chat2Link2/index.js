import React, { useEffect, useState } from 'react';
import { useTokenKeys } from '../../components/fetchTokenKeys';

const chat2page2 = () => {
  const { keys, serverAddress, isLoading } = useTokenKeys();
  const [chatLink2, setChatLink2] = useState('');


  useEffect(() => {
    // 从 localStorage 中获取 chat_link2
    let link = localStorage.getItem('chat_link2');

    // 如果 chat_link2 不存在，则使用 chat_link
    if (!link) {
      link = localStorage.getItem('chat_link');
    }

    setChatLink2(link);
  }, []);

  const comLink = (key) => {
    if (!chatLink2 || !serverAddress || !key) return '';
    return `${chatLink2}/#/?settings={"key":"sk-${key}","url":"${encodeURIComponent(serverAddress)}"}`;
  };

  useEffect(() => {
    if (keys.length > 0) {
      const redirectLink = comLink(keys[0]);
      if (redirectLink) {
        window.location.href = redirectLink;
      }
    }
  }, [keys, chatLink2, serverAddress]);

  return (
    <div>
        <h3>正在加载，请稍候...</h3>
    </div>
  );
};

export default chat2page2;