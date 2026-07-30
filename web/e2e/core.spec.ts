import { expect, test } from '@playwright/test'

test('login and profile menus expose the same build version', async ({ page }) => {
  const build = await page.request.get('/api/v1/version').then(response => response.json())
  await page.goto('/login')
  await expect(page.getByText(`kanpic ${build.version}`)).toBeVisible()
  await page.goto('/')
  await page.locator('.profile-trigger').click()
  await expect(page.locator('.version-menu')).toContainText(`kanpic ${build.version}`)
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

test('undoes and redoes an acknowledged cell operation', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId = page.url().split('/workbooks/')[1]
  const workbook = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const sheetId = workbook.sheets[0].id as string
  const valueAtA1 = async () => {
    const response = await page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1`)
    const body = await response.json()
    return body.items[0]?.value
  }

  const canvas = page.locator('canvas.grid-canvas')
  await canvas.dblclick({ position: { x: 70, y: 42 } })
  await page.locator('input.cell-editor').fill('2')
  await page.locator('input.cell-editor').press('Enter')
  await expect.poll(valueAtA1).toBe(2)

  await canvas.dblclick({ position: { x: 70, y: 42 } })
  await page.locator('input.cell-editor').fill('3')
  await page.locator('input.cell-editor').press('Enter')
  await expect.poll(valueAtA1).toBe(3)

  const undo = page.getByRole('button', { name: '실행 취소' })
  await expect(undo).toBeEnabled()
  await undo.click()
  await expect.poll(valueAtA1).toBe(2)

  const redo = page.getByRole('button', { name: '다시 실행' })
  await expect(redo).toBeEnabled()
  await redo.click()
  await expect.poll(valueAtA1).toBe(3)
})
