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

test('manages workbook favorite, rename, duplicate, and delete from home', async ({ page }) => {
  await page.goto('/')
  const title=`홈 수명주기 ${Date.now()}`,renamed=`${title} 변경`,copyTitle=`${renamed} 복사본`
  const source = await page.request.post('/api/v1/workbooks', { data:{ title, workspace_id:'default' } }).then(response=>response.json())
  await page.request.patch(`/api/v1/sheets/${source.sheets[0].id}/cells:batch`, { data:{ base_version:1, idempotency_key:`home-seed-${source.id}`, cells:[{ row:1, column:1, value:42, style:{ bold:true } }] } })
  await page.reload()
  await expect(page.getByText(title, { exact:true })).toBeVisible()

  await page.getByRole('button', { name:`${title} 더보기` }).click()
  await page.getByRole('menuitem', { name:'즐겨찾기', exact:true }).click()
  await page.locator('.segmented').getByRole('button', { name:'즐겨찾기' }).click()
  await expect(page.getByText(title, { exact:true })).toBeVisible()
  await page.locator('.segmented').getByRole('button', { name:'최근' }).click()

  await page.getByRole('button', { name:`${title} 더보기` }).click()
  await page.getByRole('menuitem', { name:'이름 변경' }).click()
  await page.getByRole('textbox', { name:'워크북 이름' }).fill(renamed)
  await page.getByRole('button', { name:'워크북 이름 저장' }).click()
  await expect(page.getByText(renamed, { exact:true })).toBeVisible()

  await page.getByRole('button', { name:`${renamed} 더보기` }).click()
  await page.getByRole('menuitem', { name:'복제' }).click()
  await expect(page.getByText(copyTitle, { exact:true })).toBeVisible()
  const books=await page.request.get('/api/v1/workbooks').then(response=>response.json())
  const copied=books.items.find((workbook:{title:string})=>workbook.title===copyTitle)
  const copiedRange=await page.request.get(`/api/v1/sheets/${copied.sheets[0].id}/ranges/A1`).then(response=>response.json())
  expect(copied.version).toBe(1)
  expect(copiedRange.items[0]?.value).toBe(42)
  expect(copiedRange.items[0]?.style?.bold).toBe(true)

  await page.getByRole('button', { name:`${renamed} 더보기` }).click()
  page.once('dialog',dialog=>dialog.accept())
  await page.getByRole('menuitem', { name:'삭제' }).click()
  await expect(page.getByText(renamed, { exact:true })).toHaveCount(0)
  await expect(page.getByText(copyTitle, { exact:true })).toBeVisible()
  await page.request.delete(`/api/v1/workbooks/${copied.id}`)
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
  await expect(page.locator('.name-box')).toHaveValue('A1:A2')

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

test('merges cells without data loss and supports undo redo and offline unmerge', async ({ page, context }) => {
  await page.goto('/')
  await page.getByRole('button', { name:'새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  const canvas=page.locator('canvas.grid-canvas')
  const edit=async(position:{x:number;y:number},value:string)=>{await canvas.dblclick({position});await page.locator('input.cell-editor').fill(value);await page.locator('input.cell-editor').press('Enter')}
  const cells=async()=>page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1:B2`).then(response=>response.json())

  await edit({x:70,y:42},'merged title')
  await edit({x:170,y:69},'kept')
  await canvas.click({position:{x:70,y:42}})
  await page.keyboard.press('Shift+ArrowRight')
  await page.keyboard.press('Shift+ArrowDown')
  await page.getByRole('button',{name:'셀 병합'}).click()
  await expect.poll(async()=>{const result=await cells();return result.items.filter((cell:{style?:Record<string,unknown>})=>cell.style?.merge).length}).toBe(4)
  let merged=await cells()
  expect(merged.items.find((cell:{row:number;column:number})=>cell.row===1&&cell.column===1).value).toBe('merged title')
  expect(merged.items.find((cell:{row:number;column:number})=>cell.row===2&&cell.column===2).value).toBe('kept')

  await canvas.click({position:{x:170,y:69}})
  await expect(page.locator('.name-box')).toHaveValue('A1:B2')
  await expect(page.getByLabel('수식 입력창')).toHaveValue('merged title')
  await page.getByRole('button',{name:'실행 취소'}).click()
  await expect.poll(async()=>{const result=await cells();return result.items.some((cell:{style?:Record<string,unknown>})=>cell.style?.merge)}).toBe(false)
  await page.getByRole('button',{name:'다시 실행'}).click()
  await expect.poll(async()=>{const result=await cells();return result.items.filter((cell:{style?:Record<string,unknown>})=>cell.style?.merge).length}).toBe(4)

  await canvas.click({position:{x:170,y:69}})
  await context.setOffline(true)
  await page.getByRole('button',{name:'병합 해제'}).click()
  await expect(page.getByText('오프라인 · 로컬 저장',{exact:true})).toBeVisible()
  await context.setOffline(false)
  await expect.poll(async()=>{const result=await cells();return result.items.some((cell:{style?:Record<string,unknown>})=>cell.style?.merge)},{timeout:15_000}).toBe(false)
  merged=await cells()
  expect(merged.items.find((cell:{row:number;column:number})=>cell.row===2&&cell.column===2).value).toBe('kept')
})

test('sorts a range by multiple keys with formulas, undo, and offline resend', async ({ page, context }) => {
  await page.goto('/')
  await page.getByRole('button', { name:'새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  const seed=await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`,{data:{
    base_version:1,
    idempotency_key:`sort-seed-${workbookId}`,
    cells:[
      {row:1,column:1,value:'Name'},{row:1,column:2,value:'Quantity'},{row:1,column:3,value:'Total'},
      {row:2,column:1,value:'beta',style:{bold:true}},{row:2,column:2,value:2},{row:2,column:3,formula:'=B2*2'},
      {row:3,column:1,value:'Alpha'},{row:3,column:2,value:10},{row:3,column:3,formula:'=B3*2'},
      {row:4,column:1,value:'alpha'},{row:4,column:2,value:5},{row:4,column:3,formula:'=B4*2'},
    ],
  }})
  expect(seed.ok()).toBe(true)
  await page.reload()
  const canvas=page.locator('canvas.grid-canvas')
  await expect(canvas).toBeVisible()
  const range=async()=>page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1:C4`).then(response=>response.json())
  const rows=async()=>{
    const result=await range()
    return Array.from({length:4},(_,offset)=>{
      const row=offset+1
      const at=(column:number)=>result.items.find((cell:{row:number;column:number})=>cell.row===row&&cell.column===column)
      return {name:at(1)?.value,quantity:at(2)?.value,total:at(3)?.value,formula:at(3)?.formula,bold:at(1)?.style?.bold}
    })
  }
  const selectRange=async()=>{
    await canvas.click({position:{x:70,y:42}})
    await page.keyboard.press('Shift+ArrowRight')
    await page.keyboard.press('Shift+ArrowRight')
    await page.keyboard.press('Shift+ArrowDown')
    await page.keyboard.press('Shift+ArrowDown')
    await page.keyboard.press('Shift+ArrowDown')
    await expect(page.locator('.name-box')).toHaveValue('A1:C4')
  }

  await selectRange()
  await page.getByRole('button',{name:'범위 정렬'}).click()
  await expect(page.getByRole('dialog',{name:'범위 정렬'})).toBeVisible()
  await page.getByRole('button',{name:'+ 기준 추가'}).click()
  await page.getByLabel('2차 정렬 방향').selectOption('desc')
  await page.getByRole('button',{name:'정렬 적용'}).click()
  await expect.poll(async()=>(await rows()).map(row=>row.name)).toEqual(['Name','Alpha','alpha','beta'])
  let sorted=await rows()
  expect(sorted.map(row=>row.quantity)).toEqual(['Quantity',10,5,2])
  expect(sorted.slice(1).map(row=>row.formula)).toEqual(['=B2*2','=B3*2','=B4*2'])
  expect(sorted.slice(1).map(row=>row.total)).toEqual([20,10,4])
  expect(sorted[3].bold).toBe(true)

  await page.getByRole('button',{name:'실행 취소'}).click()
  await expect.poll(async()=>(await rows()).map(row=>row.name)).toEqual(['Name','beta','Alpha','alpha'])

  await selectRange()
  await context.setOffline(true)
  await page.getByRole('button',{name:'범위 정렬'}).click()
  await page.getByLabel('1차 정렬 열').selectOption('2')
  await page.getByLabel('1차 정렬 방향').selectOption('desc')
  await page.getByRole('button',{name:'정렬 적용'}).click()
  await expect(page.getByText('오프라인 · 로컬 저장',{exact:true})).toBeVisible()
  await context.setOffline(false)
  await expect.poll(async()=>(await rows()).map(row=>row.quantity),{timeout:15_000}).toEqual(['Quantity',10,5,2])
  sorted=await rows()
  expect(sorted.slice(1).map(row=>row.formula)).toEqual(['=B2*2','=B3*2','=B4*2'])
  expect(sorted.slice(1).map(row=>row.total)).toEqual([20,10,4])
})

test('persists personal filter views and compresses filtered canvas rows', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button',{name:'새 워크북'}).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  const seed=await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`,{data:{base_version:1,idempotency_key:`filter-seed-${workbookId}`,cells:[
    {row:1,column:1,value:'Region'},{row:1,column:2,value:'Amount'},{row:1,column:3,value:'Status'},
    {row:2,column:1,value:'Seoul'},{row:2,column:2,value:12},{row:2,column:3,value:'open',style:{background:'#fef3c7'}},
    {row:3,column:1,value:'Busan'},{row:3,column:2,value:7},{row:3,column:3,value:'open',style:{background:'#fef3c7'}},
    {row:4,column:1,value:'Daejeon'},{row:4,column:2,value:20},{row:4,column:3,value:'open',style:{background:'#fef3c7'}},
    {row:5,column:1,value:'Seoul'},{row:5,column:2,value:15},{row:5,column:3,value:'closed',style:{background:'#ffffff'}},
  ]}})
  expect(seed.ok()).toBe(true)
  await page.reload()
  const canvas=page.locator('canvas.grid-canvas')
  await expect(canvas).toBeVisible()
  await canvas.click({position:{x:70,y:42}})
  await page.keyboard.press('Shift+ArrowRight');await page.keyboard.press('Shift+ArrowRight')
  for(let index=0;index<4;index++)await page.keyboard.press('Shift+ArrowDown')
  await expect(page.locator('.name-box')).toHaveValue('A1:C5')

  await page.getByRole('button',{name:'필터 보기'}).click()
  await expect(page.getByRole('dialog',{name:'필터 보기'})).toBeVisible()
  await page.getByLabel('필터 보기 이름').fill('qualified')
  await page.getByLabel('1차 필터 값').fill('Seoul, Busan')
  await page.getByRole('button',{name:/기준 추가/}).click()
  await page.getByLabel('2차 필터 조건').selectOption('greater_or_equal')
  await page.getByLabel('2차 필터 값').fill('10')
  await page.getByRole('button',{name:/기준 추가/}).click()
  await page.getByLabel('3차 필터 조건').selectOption('background_color')
  await page.getByLabel('3차 필터 색상').fill('#fef3c7')
  await page.getByRole('button',{name:'저장 및 적용'}).click()
  await expect(page.getByText('전체 4행 중 1행 표시 · 3행 숨김')).toBeVisible()
  const filterViews=async(headers?:Record<string,string>)=>page.request.get(`/api/v1/sheets/${sheetId}/filter-views`,{headers}).then(response=>response.json())
  const filterResult=async(id:string)=>page.request.post(`/api/v1/filter-views/${id}:evaluate`).then(response=>response.json())
  await expect.poll(async()=>{const result=await filterViews();return result.items[0]?.id}).not.toBeUndefined()
  const viewId=(await filterViews()).items[0].id as string
  await expect.poll(async()=>(await filterResult(viewId)).hidden_rows).toEqual([3,4,5])
  const otherUser=await filterViews({'X-Kanpic-Actor':'other-filter-user'})
  expect(otherUser.items).toEqual([])
  await page.getByRole('button',{name:'필터 닫기'}).click()

  await page.reload()
  await expect(canvas).toBeVisible()
  await expect(page.getByRole('button',{name:'필터 보기'})).toHaveClass(/active/)
  await canvas.click({position:{x:70,y:96}})
  await expect(page.locator('.name-box')).toHaveValue('A6')
  await page.getByRole('button',{name:'필터 보기'}).click()
  await page.getByRole('button',{name:/qualified.*적용 중/}).click()
  await page.getByRole('button',{name:'필터 해제'}).click()
  await expect.poll(async()=>Boolean((await filterViews()).items[0]?.active)).toBe(false)
  await page.getByRole('button',{name:'필터 닫기'}).click()
  await canvas.click({position:{x:70,y:96}})
  await expect(page.locator('.name-box')).toHaveValue('A3')

  await page.getByRole('button',{name:'필터 보기'}).click()
  await page.getByRole('button',{name:/qualified/}).click()
  await page.getByRole('button',{name:'필터 적용'}).click()
  await expect.poll(async()=>Boolean((await filterViews()).items[0]?.active)).toBe(true)
  await page.getByRole('button',{name:'필터 닫기'}).click()
  const latest=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`,{data:{base_version:latest.version,idempotency_key:`filter-latest-${workbookId}`,cells:[{row:3,column:2,value:11}]}})
  await expect.poll(async()=>(await filterResult(viewId)).hidden_rows).toEqual([4,5])
  await canvas.click({position:{x:70,y:96}})
  await expect(page.locator('.name-box')).toHaveValue('A3')
})

test('creates colored dropdown validation and rejects invalid writes', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button',{name:'새 워크북'}).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  const canvas=page.locator('canvas.grid-canvas')
  await canvas.click({position:{x:70,y:42}})
  await page.keyboard.press('Shift+ArrowDown');await page.keyboard.press('Shift+ArrowDown')
  await expect(page.locator('.name-box')).toHaveValue('A1:A3')

  await page.getByRole('button',{name:'데이터 검증'}).click()
  await expect(page.getByRole('dialog',{name:'데이터 검증'})).toBeVisible()
  await page.getByLabel('목록 항목 1 값').fill('open')
  await page.getByLabel('목록 항목 1 라벨').fill('Open')
  await page.getByLabel('목록 항목 1 색상').fill('#dcfce7')
  await page.getByRole('button',{name:/항목 추가/}).click()
  await page.getByLabel('목록 항목 2 값').fill('closed')
  await page.getByLabel('목록 항목 2 라벨').fill('Closed')
  await page.getByLabel('목록 항목 2 색상').fill('#fee2e2')
  await page.getByLabel('검증 도움말').fill('상태 목록에서 선택하세요.')
  await page.getByRole('button',{name:'규칙 저장'}).click()
  const rules=async()=>page.request.get(`/api/v1/sheets/${sheetId}/data-validations`).then(response=>response.json())
  await expect.poll(async()=>{const result=await rules();return result.items[0]?.range}).toBe('A1:A3')
  const rule=(await rules()).items[0]
  expect(rule.options.map((option:{value:string;color:string})=>[option.value,option.color])).toEqual([['open','#dcfce7'],['closed','#fee2e2']])
  await page.getByRole('button',{name:'기존 데이터 검사'}).click()
  await expect(page.getByText('검사 3셀 · 정상 3셀 · 오류 0셀')).toBeVisible()
  await page.getByRole('button',{name:'데이터 검증 닫기'}).click()

  await canvas.click({position:{x:70,y:42}})
  await page.locator('.cell-dropdown-trigger').click()
  await page.getByRole('option',{name:'드롭다운 값 Open'}).click()
  const valueAt=async(row:number)=>page.request.get(`/api/v1/sheets/${sheetId}/ranges/A${row}`).then(response=>response.json()).then(body=>body.items[0]?.value)
  await expect.poll(()=>valueAt(1)).toBe('open')
  await canvas.click({position:{x:70,y:69}})
  await page.locator('.cell-dropdown-trigger').click()
  await page.getByRole('option',{name:'드롭다운 값 Closed'}).click()
  await expect.poll(()=>valueAt(2)).toBe('closed')

  const latest=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const rejected=await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`,{data:{base_version:latest.version,idempotency_key:`validation-invalid-${workbookId}`,cells:[{row:3,column:1,value:'invalid'}]}})
  expect(rejected.status()).toBe(422)
  const rejection=await rejected.json();expect(rejection.error.code).toBe('validation_failed');expect(rejection.error.violations[0].validation_id).toBe(rule.id)
  expect(await valueAt(3)).toBeUndefined()

  await canvas.click({position:{x:70,y:96}})
  const dialogPromise=page.waitForEvent('dialog')
  const pastePromise=page.locator('.grid-viewport').evaluate(element=>{const data=new DataTransfer();data.setData('text/plain','invalid');element.dispatchEvent(new ClipboardEvent('paste',{bubbles:true,cancelable:true,clipboardData:data}))})
  const dialog=await dialogPromise
  expect(dialog.message()).toContain('상태 목록에서 선택하세요.')
  await dialog.accept()
  await pastePromise
  await expect.poll(()=>valueAt(3)).toBeUndefined()

  await page.reload();await expect(canvas).toBeVisible()
  await canvas.click({position:{x:70,y:42}})
  await expect(page.locator('.cell-dropdown-trigger')).toBeVisible()
  await page.getByRole('button',{name:'데이터 검증'}).click()
  await page.locator('.validation-layout>aside button').nth(1).click()
  const deleteDialogPromise=page.waitForEvent('dialog')
  const deletePromise=page.getByRole('button',{name:'삭제',exact:true}).click()
  const deleteDialog=await deleteDialogPromise
  await deleteDialog.accept()
  await deletePromise
  await expect.poll(async()=>(await rules()).items).toEqual([])
})

test('spills FILTER results and protects generated cells in the editor', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button',{name:'새 워크북'}).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  const canvas=page.locator('canvas.grid-canvas')
  const edit=async(position:{x:number;y:number},value:string)=>{await canvas.dblclick({position});await page.locator('input.cell-editor').fill(value);await page.locator('input.cell-editor').press('Enter')}
  const range=async()=>page.request.get(`/api/v1/sheets/${sheetId}/ranges/D1:E2`).then(response=>response.json())

  await edit({x:70,y:42},'a')
  await edit({x:170,y:42},'30')
  await edit({x:70,y:69},'b')
  await edit({x:170,y:69},'10')
  await edit({x:70,y:96},'c')
  await edit({x:170,y:96},'20')
  await edit({x:390,y:42},'=FILTER(A1:B3,B1:B3>=20)')
  await expect.poll(async()=>{
    const result=await range()
    return result.items.map((cell:{value:unknown})=>cell.value)
  }).toEqual(['a',30,'c',20])
  let result=await range()
  expect(result.items.slice(1).every((cell:{spill_source?:string})=>cell.spill_source==='D1')).toBe(true)

  await canvas.click({position:{x:390,y:69}})
  await page.keyboard.press('F2')
  await expect(page.locator('.name-box')).toHaveValue('D1')
  await expect(page.locator('input.cell-editor')).toHaveValue('=FILTER(A1:B3,B1:B3>=20)')
  await page.locator('input.cell-editor').press('Escape')

  await canvas.click({position:{x:390,y:69}})
  const dialogPromise=page.waitForEvent('dialog')
  const pastePromise=page.locator('.grid-viewport').evaluate(element=>{const data=new DataTransfer();data.setData('text/plain','invalid');element.dispatchEvent(new ClipboardEvent('paste',{bubbles:true,cancelable:true,clipboardData:data}))})
  const dialog=await dialogPromise
  expect(dialog.message()).toContain('D1 배열 수식의 결과')
  await dialog.accept()
  await pastePromise
  expect((await range()).items.map((cell:{value:unknown})=>cell.value)).toEqual(['a',30,'c',20])

  await edit({x:170,y:42},'5')
  await expect.poll(async()=>{
    const shrunk=await range()
    return shrunk.items.map((cell:{value:unknown})=>cell.value)
  }).toEqual(['c',20])
  result=await range()
  expect(result.items[1].spill_source).toBe('D1')
})

test('recalculates cross-sheet formulas entered in the editor and preserves them through rename', async ({ page }) => {
  const workbook=await page.request.post('/api/v1/workbooks',{data:{title:`교차 시트 ${Date.now()}`,workspace_id:'default'}}).then(response=>response.json())
  const inputSheet=workbook.sheets[0]
  const reportSheet=await page.request.post(`/api/v1/workbooks/${workbook.id}/sheets`,{data:{name:'Sales Report'}}).then(response=>response.json())
  await page.request.patch(`/api/v1/sheets/${inputSheet.id}/cells:batch`,{data:{base_version:2,idempotency_key:`cross-seed-${workbook.id}`,cells:[{row:1,column:1,value:10}]}})
  await page.goto(`/workbooks/${workbook.id}`)
  await page.getByRole('button',{name:'Sales Report',exact:true}).click()
  const canvas=page.locator('canvas.grid-canvas')
  await canvas.dblclick({position:{x:208,y:42}})
  await page.locator('input.cell-editor').fill(`='Sheet1'!A1*2`)
  await page.locator('input.cell-editor').press('Enter')
  const reportValue=async()=>page.request.get(`/api/v1/sheets/${reportSheet.id}/ranges/B1`).then(response=>response.json()).then(body=>body.items[0])
  await expect.poll(async()=>(await reportValue())?.value).toBe(20)

  await page.getByRole('button',{name:'Sheet1',exact:true}).click()
  await canvas.dblclick({position:{x:70,y:42}})
  await page.locator('input.cell-editor').fill('25')
  await page.locator('input.cell-editor').press('Enter')
  await expect.poll(async()=>(await reportValue())?.value).toBe(50)

  await page.getByRole('button',{name:'Sheet1 시트 메뉴'}).click()
  await page.getByRole('menuitem',{name:'이름 변경'}).click()
  await page.getByRole('textbox',{name:'시트 이름'}).fill('Raw Data')
  await page.getByRole('button',{name:'시트 이름 저장'}).click()
  await expect.poll(async()=>(await reportValue())?.formula).toBe(`='Raw Data'!A1*2`)
})

test('creates named ranges from the name box and keeps formulas valid through rename', async ({ page }) => {
  const workbook=await page.request.post('/api/v1/workbooks',{data:{title:`이름 범위 ${Date.now()}`,workspace_id:'default'}}).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`,{data:{base_version:1,idempotency_key:`named-seed-${workbook.id}`,cells:[{row:1,column:1,value:10},{row:2,column:1,value:20}]}})
  await page.goto(`/workbooks/${workbook.id}`)
  const nameBox=page.getByRole('combobox',{name:'이름 상자'})
  await nameBox.fill('A1:A2')
  await nameBox.press('Enter')
  await expect(nameBox).toHaveValue('A1:A2')
  await page.getByRole('button',{name:'이름 범위 관리'}).click()
  await page.getByRole('textbox',{name:'이름 범위 이름'}).fill('Sales_Data')
  await expect(page.getByRole('textbox',{name:'이름 범위 대상'})).toHaveValue('A1:A2')
  await page.getByRole('button',{name:'저장',exact:true}).click()
  await expect.poll(async()=>page.request.get(`/api/v1/workbooks/${workbook.id}/named-ranges`).then(response=>response.json()).then(body=>body.items[0]?.name)).toBe('Sales_Data')
  await page.getByRole('button',{name:'이름 범위 닫기'}).click()

  await nameBox.fill('B1')
  await nameBox.press('Enter')
  const canvas=page.locator('canvas.grid-canvas')
  await canvas.dblclick({position:{x:208,y:42}})
  await page.locator('input.cell-editor').fill('=SUM(Sales_Data)')
  await page.locator('input.cell-editor').press('Enter')
  const formulaCell=async()=>page.request.get(`/api/v1/sheets/${sheetId}/ranges/B1`).then(response=>response.json()).then(body=>body.items[0])
  await expect.poll(async()=>(await formulaCell())?.value).toBe(30)

  await page.getByRole('button',{name:'이름 범위 관리'}).click()
  await page.getByRole('button',{name:/Sales_Data/}).click()
  await page.getByRole('textbox',{name:'이름 범위 이름'}).fill('Revenue')
  await page.getByRole('textbox',{name:'이름 범위 대상'}).fill('A1')
  await page.getByRole('button',{name:'저장',exact:true}).click()
  await expect.poll(async()=>({formula:(await formulaCell())?.formula,value:(await formulaCell())?.value})).toEqual({formula:'=SUM(Revenue)',value:10})
  await page.getByRole('button',{name:'이름 범위 닫기'}).click()
  await nameBox.fill('Revenue')
  await nameBox.press('Enter')
  await expect(nameBox).toHaveValue('A1')

  await page.getByRole('button',{name:'이름 범위 관리'}).click()
  await page.getByRole('button',{name:/Revenue/}).click()
  page.once('dialog',dialog=>dialog.accept())
  await page.getByRole('button',{name:'삭제',exact:true}).click()
  await expect.poll(async()=>(await formulaCell())?.value).toBe('#NAME?')
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
  await expect(second.getByLabel('수식 입력창')).toHaveValue('17')

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
  await expect(page.getByLabel('수식 입력창')).toHaveValue('10')
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
  await expect(page.locator('.name-box')).toHaveValue('A1:B1')
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

test('drags the fill handle for numeric series and relative formulas with undo and offline resend', async ({ page, context }) => {
  await page.goto('/')
  await page.getByRole('button', { name:'새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  const sheetId=workbook.sheets[0].id as string
  const canvas=page.locator('canvas.grid-canvas')
  const edit=async(position:{x:number;y:number},value:string)=>{await canvas.dblclick({position});await page.locator('input.cell-editor').fill(value);await page.locator('input.cell-editor').press('Enter')}
  const dragFill=async(handle:{x:number;y:number},target:{x:number;y:number})=>{const box=await canvas.boundingBox();if(!box)throw new Error('canvas is not visible');await page.mouse.move(box.x+handle.x,box.y+handle.y);await page.mouse.down();await page.mouse.move(box.x+target.x,box.y+target.y,{steps:6});await page.mouse.up()}
  const values=async(range:string)=>page.request.get(`/api/v1/sheets/${sheetId}/ranges/${range}`).then(response=>response.json())

  await edit({x:70,y:42},'1')
  await edit({x:70,y:69},'2')
  await canvas.click({position:{x:70,y:42}})
  await page.keyboard.press('Shift+ArrowDown')
  await dragFill({x:153,y:80},{x:100,y:149})
  await expect.poll(async()=>(await values('A1:A5')).items.map((cell:{value:unknown})=>cell.value)).toEqual([1,2,3,4,5])

  await edit({x:170,y:42},'=A1*10')
  await canvas.click({position:{x:170,y:42}})
  await dragFill({x:261,y:53},{x:208,y:149})
  await expect.poll(async()=>(await values('B1:B5')).items.map((cell:{value:unknown})=>cell.value)).toEqual([10,20,30,40,50])
  const formulas=await values('B1:B5')
  expect(formulas.items.map((cell:{formula?:string})=>cell.formula)).toEqual(['=A1*10','=A2*10','=A3*10','=A4*10','=A5*10'])

  await page.getByRole('button',{name:'실행 취소'}).click()
  await expect.poll(async()=>(await values('B1:B5')).items.map((cell:{value:unknown})=>cell.value)).toEqual([10])

  await canvas.click({position:{x:70,y:42}})
  await page.keyboard.press('Shift+ArrowDown')
  await context.setOffline(true)
  await dragFill({x:153,y:80},{x:100,y:176})
  await expect(page.getByText('오프라인 · 로컬 저장',{exact:true})).toBeVisible()
  await context.setOffline(false)
  await expect.poll(async()=>(await values('A6')).items[0]?.value,{timeout:15_000}).toBe(6)
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
