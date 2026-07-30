import { expect, test } from '@playwright/test'

test('login and profile menus expose the same build version', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByText(/kanpic v0\.1\.0/)).toBeVisible()
  await page.goto('/')
  await page.locator('.profile-trigger').click()
  await expect(page.locator('.version-menu')).toContainText('kanpic v0.1.0')
})

test('admin console and personal settings are separate surfaces', async ({ page }) => {
  await page.goto('/admin')
  await expect(page.getByRole('heading', { name: '시스템 설정' })).toBeVisible()
  await expect(page.getByText('Keycloak OIDC 간편 연결')).toBeVisible()
  await page.getByRole('button', { name: /서버 로그/ }).click()
  await expect(page.getByRole('heading', { name: '서버 로그' })).toBeVisible()
  await page.goto('/preferences')
  await expect(page.getByRole('heading', { name: '나만의 작업 환경' })).toBeVisible()
  await page.getByRole('button', { name: 'API 키' }).click()
  await expect(page.getByRole('heading', { name: '개인 API 키' })).toBeVisible()
})

test('creates a workbook and opens the virtual canvas editor', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  await expect(page.locator('canvas.grid-canvas')).toBeVisible()
  await expect(page.locator('.formula-bar')).toBeVisible()
  await expect(page.getByText('AI 도우미')).toBeVisible()
  await page.screenshot({ path: 'test-results/kanpic-editor.png', fullPage: true })
})
