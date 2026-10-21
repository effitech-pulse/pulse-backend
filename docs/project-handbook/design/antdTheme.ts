import type { ThemeConfig } from 'antd';
import { designTokens as t } from './designTokens';

export const antdTheme: ThemeConfig = {
  token: {
    colorPrimary: t.blue,
    colorInfo: t.blue,
    colorText: t.ink,
    colorTextSecondary: t.muted,
    colorBorderSecondary: t.border,
    colorBgLayout: t.canvas,
    borderRadius: t.radius,
    controlHeight: 40,
    fontSize: 14,
    fontWeightStrong: 500,
    lineHeight: 20 / 14,
    fontFamily: "Roboto, -apple-system, BlinkMacSystemFont, 'Segoe UI', Arial, sans-serif",
  },
  components: {
    Table: { headerBg: '#f8fafc', headerColor: t.muted },
    Layout: { siderBg: t.surface, headerBg: t.surface, headerHeight: 56 },
    Menu: {
      itemSelectedBg: t.selected,
      itemSelectedColor: t.blue,
      itemColor: t.ink,
      itemBorderRadius: t.radius,
      itemMarginInline: 8,
    },
    Card: { headerFontSize: 13, bodyPadding: 20 },
    Input: { controlHeight: 44 },
    Select: { controlHeight: 44 },
    Tabs: { horizontalItemGutter: 32 },
  },
};
