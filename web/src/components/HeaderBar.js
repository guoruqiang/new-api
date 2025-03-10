import React, { useContext, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { UserContext } from '../context/User';
import { useSetTheme, useTheme } from '../context/Theme';
import { useTranslation } from 'react-i18next';

import { API, getLogo, getSystemName, isMobile, showSuccess } from '../helpers';
import '../index.css';

import fireworks from 'react-fireworks';

import {
  IconClose,
  IconHelpCircle,
  IconHome,
  IconHomeStroked, IconIndentLeft,
  IconComment,
  IconCommentStroked,
  IconKey, IconMenu,
  IconNoteMoneyStroked,
  IconPriceTag,
  IconUser,
  IconLanguage,
  IconInfoCircle,
  IconCreditCard,
  IconTerminal
} from '@douyinfe/semi-icons';
import { Avatar, Button, Dropdown, Layout, Nav, Switch, Tag } from '@douyinfe/semi-ui';
import { stringToColor } from '../helpers/render';
import Text from '@douyinfe/semi-ui/lib/es/typography/text';
import { StyleContext } from '../context/Style/index.js';
import { StatusContext } from '../context/Status/index.js';

// 自定义顶部栏样式
const headerStyle = {
  boxShadow: '0 2px 10px rgba(0, 0, 0, 0.1)',
  borderBottom: '1px solid var(--semi-color-border)',
  background: 'var(--semi-color-bg-0)',
  transition: 'all 0.3s ease',
  width: '100%'
};

// 自定义顶部栏按钮样式
const headerItemStyle = {
  borderRadius: '4px',
  margin: '0 4px',
  transition: 'all 0.3s ease'
};

// 自定义顶部栏按钮悬停样式
const headerItemHoverStyle = {
  backgroundColor: 'var(--semi-color-primary-light-default)',
  color: 'var(--semi-color-primary)'
};

// 自定义顶部栏Logo样式
const logoStyle = {
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  padding: '0 10px',
  height: '100%'
};

// 自定义顶部栏系统名称样式
const systemNameStyle = {
  fontWeight: 'bold',
  fontSize: '18px',
  background: 'linear-gradient(45deg, var(--semi-color-primary), var(--semi-color-secondary))',
  WebkitBackgroundClip: 'text',
  WebkitTextFillColor: 'transparent',
  padding: '0 5px'
};

// 自定义顶部栏按钮图标样式
const headerIconStyle = {
  fontSize: '18px',
  transition: 'all 0.3s ease'
};

// 自定义头像样式
const avatarStyle = {
  margin: '4px',
  cursor: 'pointer',
  boxShadow: '0 2px 8px rgba(0, 0, 0, 0.1)',
  transition: 'all 0.3s ease'
};

// 自定义下拉菜单样式
const dropdownStyle = {
  borderRadius: '8px',
  boxShadow: '0 4px 12px rgba(0, 0, 0, 0.15)',
  overflow: 'hidden'
};

// 自定义主题切换开关样式
const switchStyle = {
  margin: '0 8px'
};

const HeaderBar = () => {
  const { t, i18n } = useTranslation();
  const [userState, userDispatch] = useContext(UserContext);
  const [styleState, styleDispatch] = useContext(StyleContext);
  const [statusState, statusDispatch] = useContext(StatusContext);
  let navigate = useNavigate();
  const [currentLang, setCurrentLang] = useState(i18n.language);

  const systemName = getSystemName();
  const logo = getLogo();
  const currentDate = new Date();
  // enable fireworks on new year(1.1 and 2.9-2.24)
  const isNewYear =
    (currentDate.getMonth() === 0 && currentDate.getDate() === 1);

  // Check if self-use mode is enabled
  const isSelfUseMode = statusState?.status?.self_use_mode_enabled || false;
  const docsLink = statusState?.status?.docs_link || '';
  const isDemoSiteMode = statusState?.status?.demo_site_enabled || false;

  let buttons = [
    {
      text: t('首页'),
      itemKey: 'home',
      to: '/',
      icon: <IconHome style={headerIconStyle} />,
    },
    {
      text: t('控制台'),
      itemKey: 'detail',
      to: '/',
      icon: <IconTerminal style={headerIconStyle} />,
    },
    {
      text: t('定价'),
      itemKey: 'pricing',
      to: '/pricing',
      icon: <IconPriceTag style={headerIconStyle} />,
    },
    // Only include the docs button if docsLink exists
    ...(docsLink ? [{
      text: t('文档'),
      itemKey: 'docs',
      isExternal: true,
      externalLink: docsLink,
      icon: <IconHelpCircle style={headerIconStyle} />,
    }] : []),
    {
      text: '点我聊天',
      itemKey: 'chat2link',
      to: '/chat2link',
      // className: localStorage.getItem('chat_link') && !isMobile() //移动端不显示
      className: !isMobile()
      ? 'semi-navigation-item-normal'
      : 'tableHiddle',
    },
    {
      text: t('关于'),
      itemKey: 'about',
      to: '/about',
      icon: <IconInfoCircle style={headerIconStyle} />,
    },
  ];

  async function logout() {
    await API.get('/api/user/logout');
    showSuccess(t('注销成功!'));
    userDispatch({ type: 'logout' });
    localStorage.removeItem('user');
    navigate('/login');
  }

  const handleNewYearClick = () => {
    fireworks.init('root', {});
    fireworks.start();
    setTimeout(() => {
      fireworks.stop();
      setTimeout(() => {
        window.location.reload();
      }, 10000);
    }, 3000);
  };

  const theme = useTheme();
  const setTheme = useSetTheme();

  useEffect(() => {
    if (theme === 'dark') {
      document.body.setAttribute('theme-mode', 'dark');
    } else {
      document.body.removeAttribute('theme-mode');
    }
    // 发送当前主题模式给子页面
    const iframe = document.querySelector('iframe');
    if (iframe) {
      iframe.contentWindow.postMessage({ themeMode: theme }, '*');
    }

    if (isNewYear) {
      console.log('Happy New Year!');
    }
  }, [theme]); // 监听 theme-mode 的变化

  return (
    <>
      <Layout>
        <div style={{ width: '100%' }}>
          <Nav
            className={'topnav'}
            mode={'horizontal'}
            style={headerStyle}
            itemStyle={headerItemStyle}
            hoverStyle={headerItemHoverStyle}
            renderWrapper={({ itemElement, isSubNav, isInSubNav, props }) => {
              const routerMap = {
                about: '/about',
                login: '/login',
                register: '/register',
                pricing: '/pricing',
                detail: '/detail',
                home: '/',
                chat2link: 'chat/0', // 配合 /chat/0使用
                chat: '/chat',
              };
              return (
                <div onClick={(e) => {
                  if (props.itemKey === 'home') {
                    styleDispatch({ type: 'SET_INNER_PADDING', payload: false });
                    styleDispatch({ type: 'SET_SIDER', payload: false });
                  } else {
                    styleDispatch({ type: 'SET_INNER_PADDING', payload: true });
                    if (!styleState.isMobile) {
                      styleDispatch({ type: 'SET_SIDER', payload: true });
                    }
                  }
                }}>
                  {props.isExternal ? (
                    <a
                      className="header-bar-text"
                      style={{ textDecoration: 'none' }}
                      href={props.externalLink}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {itemElement}
                    </a>
                  ) : (
                    <Link
                      className="header-bar-text"
                      style={{ textDecoration: 'none' }}
                      to={routerMap[props.itemKey]}
                      // chat2link 新标签页打开
                      target={props.itemKey === 'chat2link' ? '_blank' : undefined}
                    >
                      {itemElement}
                    </Link>
                  )}
                </div>
              );
            }}
            selectedKeys={[]}
            // items={headerButtons}
            onSelect={(key) => {}}
            header={styleState.isMobile?{
              logo: (
                <div style={{ display: 'flex', alignItems: 'center', position: 'relative' }}>
                  {
                    !styleState.showSider ?
                      <Button icon={<IconMenu />} theme="light" aria-label={t('展开侧边栏')} onClick={
                        () => styleDispatch({ type: 'SET_SIDER', payload: true })
                      } />:
                      <Button icon={<IconIndentLeft />} theme="light" aria-label={t('闭侧边栏')} onClick={
                        () => styleDispatch({ type: 'SET_SIDER', payload: false })
                      } />
                  }
                  {(isSelfUseMode || isDemoSiteMode) && (
                    <Tag 
                      color={isSelfUseMode ? 'purple' : 'blue'}
                      style={{ 
                        position: 'absolute',
                        top: '-8px',
                        right: '-15px',
                        fontSize: '0.7rem',
                        padding: '0 4px',
                        height: 'auto',
                        lineHeight: '1.2',
                        zIndex: 1,
                        pointerEvents: 'none'
                      }}
                    >
                      {isSelfUseMode ? t('自用模式') : t('演示站点')}
                    </Tag>
                  )}
                </div>
              ),
            }:{
              logo: (
                <div style={logoStyle}>
                  <img src={logo} alt='logo' style={{ height: '28px' }} />
                </div>
              ),
              text: (
                <div style={{ position: 'relative', display: 'inline-block' }}>
                  <span style={systemNameStyle}>{systemName}</span>
                  {(isSelfUseMode || isDemoSiteMode) && (
                    <Tag 
                      color={isSelfUseMode ? 'purple' : 'blue'}
                      style={{ 
                        position: 'absolute', 
                        top: '-10px', 
                        right: '-25px', 
                        fontSize: '0.7rem',
                        padding: '0 4px',
                        whiteSpace: 'nowrap',
                        zIndex: 1,
                        boxShadow: '0 0 3px rgba(255, 255, 255, 0.7)'
                      }}
                    >
                      {isSelfUseMode ? t('自用模式') : t('演示站点')}
                    </Tag>
                  )}
                </div>
              ),
            }}
            items={buttons}
            footer={
              <>
                {isNewYear && (
                  // happy new year
                  <Dropdown
                    position='bottomRight'
                    render={
                      <Dropdown.Menu style={dropdownStyle}>
                        <Dropdown.Item onClick={handleNewYearClick}>
                          Happy New Year!!!
                        </Dropdown.Item>
                      </Dropdown.Menu>
                    }
                  >
                    <Nav.Item itemKey={'new-year'} text={'🎉'} />
                  </Dropdown>
                )}
                {/* <Nav.Item itemKey={'about'} icon={<IconHelpCircle />} /> */}
                <>
                  <Switch
                    checkedText='🌞'
                    size={styleState.isMobile?'default':'large'}
                    checked={theme === 'dark'}
                    uncheckedText='🌙'
                    style={switchStyle}
                    onChange={(checked) => {
                      setTheme(checked);
                    }}
                  />
                </>
                <Dropdown
                  position='bottomRight'
                  render={
                    <Dropdown.Menu style={dropdownStyle}>
                      <Dropdown.Item
                        onClick={() => handleLanguageChange('zh')}
                        type={currentLang === 'zh' ? 'primary' : 'tertiary'}
                      >
                        中文
                      </Dropdown.Item>
                      <Dropdown.Item
                        onClick={() => handleLanguageChange('en')}
                        type={currentLang === 'en' ? 'primary' : 'tertiary'}
                      >
                        English
                      </Dropdown.Item>
                    </Dropdown.Menu>
                  }
                >
                  <Nav.Item
                    itemKey={'language'}
                    icon={<IconLanguage style={headerIconStyle} />}
                  />
                </Dropdown>
                {userState.user ? (
                  <>
                    <Dropdown
                      position='bottomRight'
                      render={
                        <Dropdown.Menu style={dropdownStyle}>
                          <Dropdown.Item onClick={logout}>{t('退出')}</Dropdown.Item>
                        </Dropdown.Menu>
                      }
                    >
                      <Avatar
                        size='small'
                        color={stringToColor(userState.user.username)}
                        style={avatarStyle}
                      >
                        {userState.user.username[0]}
                      </Avatar>
                      {styleState.isMobile?null:<Text style={{ marginLeft: '4px', fontWeight: '500' }}>{userState.user.username}</Text>}
                    </Dropdown>
                  </>
                ) : (
                  <>
                    <Nav.Item
                      itemKey={'login'}
                      text={!styleState.isMobile?t('登录'):null}
                      icon={<IconUser style={headerIconStyle} />}
                    />
                    {
                      // Hide register option in self-use mode
                      !styleState.isMobile && !isSelfUseMode && (
                        <Nav.Item
                          itemKey={'register'}
                          text={t('注册')}
                          icon={<IconKey style={headerIconStyle} />}
                        />
                      )
                    }
                  </>
                )}
              </>
            }
          ></Nav>
        </div>
      </Layout>
    </>
  );
};

export default HeaderBar;
