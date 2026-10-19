import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { ConfigProvider, theme as algorithms } from 'antd';
import ruRU from 'antd/locale/ru_RU';
import { antdTheme } from './antdTheme';

const ThemeContext = createContext({ dark: false, toggle: () => {} });
export const useTheme = () => useContext(ThemeContext);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [dark, setDark] = useState(() => {
    try { return localStorage.getItem('pulse-theme') === 'dark'; } catch { return false; }
  });
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
    try { localStorage.setItem('pulse-theme', dark ? 'dark' : 'light'); } catch { /* приватный режим */ }
  }, [dark]);
  const colors = dark ? { colorLink: '#82adff', colorLinkHover: '#aac8ff', colorLinkActive: '#82adff', colorText: '#e5edf7', colorTextSecondary: '#9eafc4', colorBgLayout: '#0c131d', colorBgContainer: '#141e2b', colorBorder: '#607893', colorBorderSecondary: '#2d3e53' } : {};
  return <ThemeContext.Provider value={{ dark, toggle: () => setDark(v => !v) }}>
    <ConfigProvider locale={ruRU} theme={{ ...antdTheme, algorithm: dark ? algorithms.darkAlgorithm : algorithms.defaultAlgorithm,
      token: { ...antdTheme.token, ...colors }, components: { ...antdTheme.components,
        Table: { headerBg: dark ? '#0c131d' : '#f3f5f7', headerColor: dark ? '#9eafc4' : '#657487' },
        Layout: { siderBg: dark ? '#0c131d' : '#f3f5f7', headerBg: dark ? '#141e2b' : '#ffffff', headerHeight: 64 },
        Menu: { itemBg: 'transparent', itemSelectedBg: dark ? '#1d3455' : '#edf3ff', itemSelectedColor: dark ? '#82adff' : '#2f6fed', itemBorderRadius: 4, itemHeight: 44, itemMarginInline: 12 },
      } }}>{children}</ConfigProvider>
  </ThemeContext.Provider>;
}
