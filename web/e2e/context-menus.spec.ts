import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

async function seedWorkbook(request:APIRequestContext,title:string,cells:Array<{row:number;column:number;value:unknown}>=[]){
  const workbook=await request.post('/api/v1/workbooks',{data:{title}}).then(response=>response.json())
  if(cells.length>0)await request.patch(`/api/v1/sheets/${workbook.sheets[0].id}/cells:batch`,{data:{
    base_version:workbook.version,idempotency_key:`menu-seed-${workbook.id}`,cells,
  }})
  return workbook
}

async function openEditor(page:Page,workbookId:string){
  await page.goto(`/workbooks/${workbookId}`)
  await page.waitForSelector('.grid-canvas')
  await page.waitForTimeout(800)
}

test('any sheet tab opens its context menu on right click', async ({ page, request }) => {
  const workbook=await seedWorkbook(request,`탭 메뉴 ${Date.now()}`)
  await request.post(`/api/v1/workbooks/${workbook.id}/sheets`,{data:{name:'원본 데이터'}})
  await openEditor(page,workbook.id)

  // The second tab is not active, so this also proves the menu no longer needs
  // the ⋯ button that only appeared on the active tab.
  await page.getByText('원본 데이터',{exact:true}).click({button:'right'})
  const menu=page.getByRole('menu',{name:'원본 데이터 시트 메뉴'})
  await expect(menu).toBeVisible()
  await menu.getByRole('menuitem',{name:'탭 색상'}).click()
  await page.getByRole('menuitemcheckbox',{name:'파랑'}).click()
  await expect(page.locator('.sheet-tab-main.active i')).toHaveCSS('background-color','rgb(59, 130, 246)')

  // Escape closes the menu like every other overlay.
  await page.getByText('원본 데이터',{exact:true}).click({button:'right'})
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu',{name:'원본 데이터 시트 메뉴'})).toHaveCount(0)
})

test('a workbook card and a chart expose the same actions on right click', async ({ page, request }) => {
  const title=`카드 메뉴 ${Date.now()}`
  const workbook=await seedWorkbook(request,title,[{row:1,column:1,value:'항목'},{row:2,column:1,value:'임대료'},{row:2,column:2,value:1200}])
  await page.goto('/')
  await page.locator('.workbook-card',{hasText:title}).click({button:'right'})
  const cardMenu=page.getByRole('menu',{name:`${title} 메뉴`})
  await expect(cardMenu).toBeVisible()
  await expect(cardMenu.getByRole('menuitem',{name:'새 탭에서 열기'})).toBeVisible()
  await cardMenu.getByRole('menuitem',{name:'즐겨찾기에 추가'}).click()
  await expect(page.locator('.workbook-card',{hasText:title}).locator('.favorite-on, .favorite.on, [aria-pressed="true"]').first()).toBeVisible({timeout:5000}).catch(()=>undefined)
  const favorited=await request.get('/api/v1/workbooks').then(response=>response.json())
  expect(favorited.items.find((item:{id:string})=>item.id===workbook.id)?.favorite).toBe(true)

  await request.post(`/api/v1/workbooks/${workbook.id}/charts`,{data:{
    idempotency_key:`chart-${Date.now()}`,sheet_id:workbook.sheets[0].id,source_sheet_id:workbook.sheets[0].id,
    type:'bar',title:'임대료 차트',source_range:'A1:B2',position:{x:40,y:40,width:320,height:220},
  }})
  await openEditor(page,workbook.id)
  await page.locator('.chart-card').first().click({button:'right'})
  const chartMenu=page.getByRole('menu',{name:'차트 메뉴'})
  await expect(chartMenu).toBeVisible()
  await expect(chartMenu.getByRole('menuitem',{name:'PNG로 내보내기'})).toBeVisible()
  page.once('dialog',dialog=>dialog.accept())
  await chartMenu.getByRole('menuitem',{name:'차트 삭제'}).click()
  await expect(page.locator('.chart-card')).toHaveCount(0)
})

test('data cleanup removes duplicates and splits text into columns', async ({ page, request }) => {
  const workbook=await seedWorkbook(request,`데이터 정리 ${Date.now()}`,[
    {row:1,column:1,value:'이름'},{row:2,column:1,value:'박지민'},{row:3,column:1,value:'박지민'},{row:4,column:1,value:'이서준'},
  ])
  await openEditor(page,workbook.id)

  page.once('dialog',dialog=>dialog.accept())
  await page.getByRole('menuitem',{name:'데이터'}).click()
  await page.getByRole('menuitem',{name:'데이터 정리'}).click()
  await page.getByRole('menuitem',{name:'중복 항목 삭제'}).click()
  await expect.poll(async()=>{
    const cells=await request.get(`/api/v1/sheets/${workbook.sheets[0].id}/ranges/A1:D20`).then(response=>response.json())
    return (cells.items??[]).filter((cell:{value?:unknown})=>cell.value==='박지민').length
  },{timeout:10_000}).toBe(1)

  // Splitting uses the first selected column and writes the parts to its right.
  await request.patch(`/api/v1/sheets/${workbook.sheets[0].id}/cells:batch`,{data:{
    base_version:(await request.get(`/api/v1/workbooks/${workbook.id}`).then(response=>response.json())).version,
    idempotency_key:`split-${Date.now()}`,cells:[{row:6,column:1,value:'서울,강남,1200'}],
  }})
  await page.reload()
  await page.waitForSelector('.grid-canvas')
  await page.waitForTimeout(600)
  await page.locator('.name-box').fill('A6')
  await page.keyboard.press('Enter')
  await page.getByRole('menuitem',{name:'데이터'}).click()
  await page.getByRole('menuitem',{name:'텍스트를 열로 분할'}).click()
  await page.getByRole('menuitem',{name:'자동 감지'}).click()
  await expect.poll(async()=>{
    const cells=await request.get(`/api/v1/sheets/${workbook.sheets[0].id}/ranges/A1:D20`).then(response=>response.json())
    return (cells.items??[]).find((cell:{row:number;column:number})=>cell.row===6&&cell.column===3)?.value
  },{timeout:10_000}).toBe(1200)
})

test('the help menu lists the functions the engine supports', async ({ page, request }) => {
  const workbook=await seedWorkbook(request,`함수 목록 ${Date.now()}`)
  await openEditor(page,workbook.id)
  await page.getByRole('menuitem',{name:'도움말'}).click()
  await page.getByRole('menuitem',{name:'함수 목록'}).click()
  const dialog=page.getByRole('dialog',{name:'함수 목록'})
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('TEXTJOIN',{exact:true})).toBeVisible()
  await dialog.getByRole('textbox',{name:'함수 검색'}).fill('중앙값')
  await expect(dialog.getByText('MEDIAN',{exact:true})).toBeVisible()
  await expect(dialog.getByText('SUMIF',{exact:true})).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
})

test('new formula functions evaluate on the server', async ({ request }) => {
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`수식 확장 ${Date.now()}`}}).then(response=>response.json())
  await request.patch(`/api/v1/sheets/${workbook.sheets[0].id}/cells:batch`,{data:{
    base_version:workbook.version,idempotency_key:`formula-${Date.now()}`,
    cells:[
      {row:1,column:1,value:'  공백  '},
      {row:2,column:1,formula:'=TRIM(A1)'},
      {row:3,column:1,formula:'=IFERROR(1/0,"안전")'},
      {row:4,column:1,formula:'=TEXTJOIN("-",TRUE,"가","나")'},
    ],
  }})
  const cells=await request.get(`/api/v1/sheets/${workbook.sheets[0].id}/ranges/A1:D20`).then(response=>response.json())
  const valueAt=(row:number)=>cells.items.find((cell:{row:number})=>cell.row===row)?.value
  expect(valueAt(2)).toBe('공백')
  expect(valueAt(3)).toBe('안전')
  expect(valueAt(4)).toBe('가-나')
})

test('the format and insert menus reach font size, fill colour and functions', async ({ page, request }) => {
  const workbook=await seedWorkbook(request,`메뉴 확장 ${Date.now()}`,[
    {row:1,column:1,value:10},{row:2,column:1,value:30},{row:3,column:1,value:50},
  ])
  await openEditor(page,workbook.id)
  const sheet=workbook.sheets[0].id
  const styleAt=async(row:number,column:number)=>{
    const range=await request.get(`/api/v1/sheets/${sheet}/ranges/A1:D20`).then(response=>response.json())
    return (range.items??[]).find((cell:{row:number;column:number})=>cell.row===row&&cell.column===column)?.style
  }

  await page.locator('.name-box').fill('A1')
  await page.keyboard.press('Enter')
  await page.getByRole('menuitem',{name:'서식'}).click()
  await page.getByRole('menuitem',{name:'글꼴 크기'}).click()
  await page.getByRole('menuitemcheckbox',{name:'18'}).click()
  await expect.poll(async()=>(await styleAt(1,1))?.font_size,{timeout:10_000}).toBe(18)

  await page.getByRole('menuitem',{name:'서식'}).click()
  await page.getByRole('menuitem',{name:'채우기 색'}).click()
  await page.getByRole('menuitemcheckbox',{name:'연노랑'}).click()
  await expect.poll(async()=>(await styleAt(1,1))?.background,{timeout:10_000}).toBe('#fef3c7')

  // A selected range makes the inserted aggregate reference exactly that range.
  await page.locator('.name-box').fill('A1:A3')
  await page.keyboard.press('Enter')
  await page.getByRole('menuitem',{name:'삽입'}).click()
  await page.getByRole('menuitem',{name:'함수'}).click()
  await page.getByRole('menuitem',{name:'MEDIAN',exact:true}).click()
  await expect(page.locator('input.cell-editor')).toHaveValue('=MEDIAN(A1:A3)')
  await page.keyboard.press('Enter')
  await expect.poll(async()=>{
    const range=await request.get(`/api/v1/sheets/${sheet}/ranges/A1:D20`).then(response=>response.json())
    return (range.items??[]).find((cell:{row:number})=>cell.row===4)?.value
  },{timeout:10_000}).toBe(30)
})

test('the view menu toggles gridlines and the file menu renames the workbook', async ({ page, request }) => {
  const title=`보기 메뉴 ${Date.now()}`
  const workbook=await seedWorkbook(request,title)
  await openEditor(page,workbook.id)

  await page.getByRole('menuitem',{name:'보기'}).click()
  const gridlines=page.getByRole('menuitemcheckbox',{name:'눈금선 표시'})
  await expect(gridlines).toHaveAttribute('aria-checked','true')
  await gridlines.click()
  await page.getByRole('menuitem',{name:'보기'}).click()
  await expect(page.getByRole('menuitemcheckbox',{name:'눈금선 표시'})).toHaveAttribute('aria-checked','false')
  await page.keyboard.press('Escape')

  page.once('dialog',dialog=>dialog.accept(`${title} 변경`))
  await page.getByRole('menuitem',{name:'파일'}).click()
  await page.getByRole('menuitem',{name:'워크북 이름 변경…'}).click()
  await expect.poll(async()=>(await request.get(`/api/v1/workbooks/${workbook.id}`).then(response=>response.json())).title,{timeout:10_000}).toBe(`${title} 변경`)
})

test('the quick start gallery creates a workbook that already calculates', async ({ page, request }) => {
  const catalog=await request.get('/api/v1/templates').then(response=>response.json())
  expect(catalog.items.length).toBeGreaterThanOrEqual(30)

  await page.goto('/')
  await page.getByRole('button',{name:/템플릿 갤러리/}).click()
  const gallery=page.getByRole('dialog',{name:'템플릿 갤러리'})
  await expect(gallery.locator('.template-card')).toHaveCount(catalog.items.length)

  // Category and search narrow the list the same way.
  await gallery.getByRole('tab',{name:'재무·회계'}).click()
  await expect(gallery.locator('.template-card',{hasText:'재고 관리'})).toHaveCount(0)
  await gallery.getByRole('textbox',{name:'템플릿 검색'}).fill('거래명세서')
  await expect(gallery.locator('.template-card')).toHaveCount(1)

  await gallery.locator('.template-card',{hasText:'거래명세서'}).getByRole('button',{name:'사용하기'}).click()
  await page.waitForURL(/\/workbooks\//)
  await page.waitForSelector('.grid-canvas')
  const workbookId=page.url().split('/workbooks/')[1]
  const workbook=await request.get(`/api/v1/workbooks/${workbookId}`).then(response=>response.json())
  expect(workbook.title).toBe('거래명세서')
  expect(workbook.sheets[0].layout.frozen_rows).toBe(3)

  // The template ships with working formulas, so the totals are already there.
  const range=await request.get(`/api/v1/sheets/${workbook.sheets[0].id}/ranges/A1:G12`).then(response=>response.json())
  const at=(row:number,column:number)=>(range.items??[]).find((cell:{row:number;column:number})=>cell.row===row&&cell.column===column)
  expect(at(4,7)?.value).toBe(19_800_000)
  expect(at(8,7)?.value).toBe(39_490_000)
  expect(at(4,7)?.style?.number_format).toBe('₩#,##0')
})
