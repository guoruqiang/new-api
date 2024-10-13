import React, { useEffect, useState } from 'react';
import { useTokenKeys } from '../../components/fetchTokenKeys';

const chat2page = () => {
  const { keys, serverAddress, isLoading } = useTokenKeys();
  const [chatLink, setChatLink] = useState('');

  useEffect(() => {
    const link = localStorage.getItem('chat_link');
    setChatLink(link);
  }, []);

  const comLink = (key) => {
    if (!chatLink || !serverAddress || !key) return '';
    return `${chatLink}/#/?settings={"key":"sk-${key}","url":"${encodeURIComponent(serverAddress)}"}`;
  };

  useEffect(() => {
    console.log('Keys:', keys);
    console.log('ChatLink:', chatLink);
    console.log('ServerAddress:', serverAddress);

    if (keys.length > 0) {
      const redirectLink = comLink(keys[0]);
      console.log('RedirectLink:', redirectLink);
      if (redirectLink) {
        window.location.href = redirectLink;
      }
    }
  }, [keys, chatLink, serverAddress]);

  return (
    <div>
      <h3>正在加载，请稍候...</h3>
    </div>
  );
};

export default chat2page;