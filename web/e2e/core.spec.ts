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

test('formats a selected range without changing values or formulas and resends offline changes', async ({ page, context }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId = page.url().split('/workbooks/')[1]
  const workbook = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const sheetId = workbook.sheets[0].id as string
  const canvas = page.locator('canvas.grid-canvas')
  const edit = async (position:{x:number;y:number}, value:string) => {
    await canvas.dblclick({ position })
    await page.locator('input.cell-editor').fill(value)
    await page.locator('input.cell-editor').press('Enter')
  }
  const range = async () => page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1:A2`).then(response => response.json())

  await edit({ x:70, y:42 }, '5')
  await edit({ x:70, y:69 }, '=A1*2')
  await expect.poll(async () => (await range()).items.map((cell:{value:unknown}) => cell.value)).toEqual([5, 10])
  await canvas.click({ position:{ x:70, y:42 } })
  await page.keyboard.press('Shift+ArrowDown')
  await expect(page.locator('.name-box')).toHaveText('A1:A2')

  await page.getByRole('button', { name:'굵게' }).click()
  await expect.poll(async () => (await range()).items.map((cell:{style?:Record<string,unknown>}) => cell.style?.bold)).toEqual([true, true])
  await page.getByRole('button', { name:'가운데 정렬' }).click()
  await expect.poll(async () => (await range()).items.map((cell:{style?:Record<string,unknown>}) => cell.style?.horizontal_align)).toEqual(['center', 'center'])
  await page.getByLabel('셀 배경색').fill('#fef3c7')
  await expect.poll(async () => (await range()).items.map((cell:{style?:Record<string,unknown>}) => cell.style?.background)).toEqual(['#fef3c7', '#fef3c7'])
  await page.getByLabel('글꼴 크기').selectOption('14')
  await expect.poll(async () => (await range()).items.map((cell:{style?:Record<string,unknown>}) => cell.style?.font_size)).toEqual([14, 14])

  let result = await range()
  expect(result.items.map((cell:{value:unknown}) => cell.value)).toEqual([5, 10])
  expect(result.items[1].formula).toBe('=A1*2')
  await page.getByRole('button', { name:'실행 취소' }).click()
  await expect.poll(async () => (await range()).items.map((cell:{style?:Record<string,unknown>}) => cell.style?.font_size)).toEqual([undefined, undefined])
  result = await range()
  expect(result.items.map((cell:{value:unknown}) => cell.value)).toEqual([5, 10])
  expect(result.items[1].formula).toBe('=A1*2')

  await context.setOffline(true)
  await page.getByRole('button', { name:'기울임' }).click()
  await expect(page.getByText('오프라인 · 로컬 저장', { exact:true })).toBeVisible()
  await context.setOffline(false)
  await expect.poll(async () => (await range()).items.map((cell:{style?:Record<string,unknown>}) => cell.style?.italic), { timeout:15_000 }).toEqual([true, true])
})

test('synchronizes presence and edits between two browser tabs', async ({ page, context }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const editorURL = page.url()
  await expect(page.locator('.collaboration-count')).toContainText('1명 접속')

  const second = await context.newPage()
  await second.goto(editorURL)
  await expect(second.locator('.collaboration-count')).toContainText('2명 접속')
  await expect(page.locator('.collaboration-count')).toContainText('2명 접속')

  const firstCanvas = page.locator('canvas.grid-canvas')
  await firstCanvas.dblclick({ position: { x: 70, y: 42 } })
  await page.locator('input.cell-editor').fill('17')
  await page.locator('input.cell-editor').press('Enter')
  await expect(second.locator('.formula-bar input')).toHaveValue('17')

  await second.close()
  await expect(page.locator('.collaboration-count')).toContainText('1명 접속')
})

test('creates a named version and restores it with an automatic backup', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId = page.url().split('/workbooks/')[1]
  const workbook = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const sheetId = workbook.sheets[0].id as string
  const valueAtA1 = async () => {
    const body = await page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1`).then(response => response.json())
    return body.items[0]?.value
  }
  const editA1 = async (value:string) => {
    const canvas = page.locator('canvas.grid-canvas')
    await canvas.dblclick({ position: { x: 70, y: 42 } })
    await page.locator('input.cell-editor').fill(value)
    await page.locator('input.cell-editor').press('Enter')
  }

  await editA1('10')
  await expect.poll(valueAtA1).toBe(10)
  await page.locator('.toolbar').getByRole('button', { name: '버전 이력', exact: true }).click()
  await expect(page.getByText('버전 이력', { exact: true })).toBeVisible()
  await page.getByPlaceholder('예: 2026년 3분기 확정').fill('기준 버전')
  await page.getByRole('button', { name: '저장', exact: true }).click()
  await expect(page.locator('.workbook-version').filter({ hasText: '기준 버전' })).toBeVisible()

  await page.locator('.toolbar').getByRole('button', { name: '버전 이력', exact: true }).click()
  await editA1('20')
  await expect.poll(valueAtA1).toBe(20)
  await page.locator('.toolbar').getByRole('button', { name: '버전 이력', exact: true }).click()
  page.once('dialog', dialog => dialog.accept())
  await page.locator('.workbook-version').filter({ hasText: '기준 버전' }).getByRole('button', { name: '복원' }).click()
  await expect.poll(valueAtA1).toBe(10)
  await expect(page.locator('.formula-bar input')).toHaveValue('10')
  await expect(page.locator('.workbook-version').filter({ hasText: '복원 전 자동 백업' })).toBeVisible()
})

test('selects a range and pastes copied formulas with relative references', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId = page.url().split('/workbooks/')[1]
  const workbook = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const sheetId = workbook.sheets[0].id as string
  const canvas = page.locator('canvas.grid-canvas')
  const edit = async (position:{x:number;y:number},value:string) => {
    await canvas.dblclick({ position })
    await page.locator('input.cell-editor').fill(value)
    await page.locator('input.cell-editor').press('Enter')
  }
  await edit({x:70,y:42},'2')
  await edit({x:170,y:42},'=A1*2')

  await canvas.click({position:{x:70,y:42}})
  await page.keyboard.press('Shift+ArrowRight')
  await expect(page.locator('.name-box')).toHaveText('A1:B1')
  await page.keyboard.press('Control+C')
  await canvas.click({position:{x:390,y:120}})
  await page.keyboard.press('Control+V')

  const pasted = async () => page.request.get(`/api/v1/sheets/${sheetId}/ranges/D4:E4`).then(response => response.json())
  await expect.poll(async()=>{
    const result=await pasted()
    return result.items.map((cell:{value:unknown})=>cell.value)
  }).toEqual([2,4])
  const result=await pasted()
  expect(result.items[1].formula).toBe('=D4*2')
})

test('pastes more than 1000 cells without truncation in one version', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId = page.url().split('/workbooks/')[1]
  const workbook = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const sheetId = workbook.sheets[0].id as string
  const text=Array.from({length:1001},(_,index)=>String(index+1)).join('\n')
  await page.locator('.grid-viewport').evaluate((element,pasteText)=>{
    const clipboardData=new DataTransfer()
    clipboardData.setData('text/plain',pasteText)
    element.dispatchEvent(new ClipboardEvent('paste',{bubbles:true,cancelable:true,clipboardData}))
  },text)
  await expect.poll(async()=>{
    const result=await page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1001`).then(response=>response.json())
    return result.items[0]?.value
  }).toBe(1001)
  const updated=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  expect(updated.version).toBe(2)
})

test('manages the complete sheet lifecycle without losing copied cells', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]

  await page.getByRole('button',{name:'시트 추가'}).click()
  await expect(page.getByRole('button',{name:'Sheet2 시트 메뉴'})).toBeVisible()
  await page.getByRole('button',{name:'Sheet2 시트 메뉴'}).click()
  await page.getByRole('menuitem',{name:'이름 변경'}).click()
  await page.getByRole('textbox',{name:'시트 이름'}).fill('Raw Data')
  await page.getByRole('button',{name:'시트 이름 저장'}).click()
  await expect(page.getByRole('button',{name:'Raw Data 시트 메뉴'})).toBeVisible()

  const canvas=page.locator('canvas.grid-canvas')
  await canvas.dblclick({position:{x:70,y:42}})
  await page.locator('input.cell-editor').fill('9')
  await page.locator('input.cell-editor').press('Enter')
  await page.getByRole('button',{name:'Raw Data 시트 메뉴'}).click()
  await page.getByRole('menuitem',{name:'복제'}).click()
  await expect(page.getByRole('button',{name:'Raw Data 복사본 시트 메뉴'})).toBeVisible()

  const currentWorkbook=async()=>page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  await expect.poll(async()=>((await currentWorkbook()).sheets as Array<{name:string}>).map(sheet=>sheet.name)).toEqual(['Sheet1','Raw Data','Raw Data 복사본'])
  let book=await currentWorkbook()
  let copied=book.sheets.find((sheet:{name:string})=>sheet.name==='Raw Data 복사본')
  const copiedCell=await page.request.get(`/api/v1/sheets/${copied.id}/ranges/A1`).then(response=>response.json())
  expect(copiedCell.items[0]?.value).toBe(9)

  await page.getByRole('button',{name:'Raw Data 복사본 시트 메뉴'}).click()
  await page.getByRole('button',{name:'시트 색상 #3b82f6'}).click()
  await expect.poll(async()=>((await currentWorkbook()).sheets as Array<{name:string;color:string}>).find(sheet=>sheet.name==='Raw Data 복사본')?.color).toBe('#3b82f6')
  await page.getByRole('button',{name:'Raw Data 복사본 시트 메뉴'}).click()
  await page.getByRole('button',{name:'왼쪽',exact:true}).click()
  await expect.poll(async()=>((await currentWorkbook()).sheets as Array<{name:string;position:number}>).map(sheet=>`${sheet.position}:${sheet.name}`)).toEqual(['0:Sheet1','1:Raw Data 복사본','2:Raw Data'])

  await page.getByRole('button',{name:'Raw Data 복사본 시트 메뉴'}).click()
  page.once('dialog',dialog=>dialog.accept())
  await page.getByRole('menuitem',{name:'삭제'}).click()
  await expect(page.getByRole('button',{name:'Sheet1 시트 메뉴'})).toBeVisible()
  book=await currentWorkbook()
  expect(book.sheets.map((sheet:{name:string;position:number})=>`${sheet.position}:${sheet.name}`)).toEqual(['0:Sheet1','1:Raw Data'])
})
