import { expect, test } from '@playwright/test'

// 표에 이름을 붙이면 열 이름으로 가리킬 수 있다. 만들어 두고 아무도 못
// 찾으면 없는 것과 같으므로, 메뉴에서 만들고 셀에서 부르는 길을 통째로
// 확인한다. 무엇보다 행을 끼웠을 때도 그 수식이 맞아야 한다 — 그것이
// 범위로 적는 대신 표를 쓰는 까닭이기 때문이다.
test('a named table is created from the menu and answers formulas by column name', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '새 워크북' }).click()
  await page.waitForURL(/\/workbooks\//)
  const workbookId = page.url().split('/workbooks/')[1]
  const workbook = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const sheetId = workbook.sheets[0].id as string
  const cells = async (range:string) =>
    page.request.get(`/api/v1/sheets/${sheetId}/ranges/${range}`).then(response => response.json())
  const values = async (range:string) => {
    const result = await cells(range)
    return result.items.map((cell:{value:unknown})=>cell.value)
  }
  // 셀은 API로 넣는다. 화면으로 여섯 칸을 치는 것은 이 시험이 보려는 것이
  // 아니다. 넣는 자리는 PATCH 다 — POST 는 405 를 조용히 돌려준다.
  const write = async (items:Array<{row:number;column:number;value?:unknown;formula?:string}>) => {
    const current = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
    const response = await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`, {
      data: { base_version: current.version, idempotency_key: `e2e-${Date.now()}-${Math.random()}`, cells: items },
    })
    expect(response.status(), await response.text()).toBeLessThan(300)
  }
  await write([
    {row:1,column:1,value:'지역'},{row:1,column:2,value:'금액'},
    {row:2,column:1,value:'서울'},{row:2,column:2,value:100},
    {row:3,column:1,value:'부산'},{row:3,column:2,value:200},
  ])
  await expect.poll(()=>values('B2:B3'),{timeout:15_000}).toEqual([100,200])

  // 삽입 메뉴에서 표를 만든다.
  await page.getByRole('menuitem', { name: '삽입' }).click()
  await page.getByRole('menuitem', { name: '표…' }).click()
  const dialog = page.getByRole('dialog', { name: '표' })
  await dialog.getByLabel('표 이름').fill('매출표')
  await dialog.getByLabel('표 범위').fill('A1:B3')
  await dialog.getByRole('button', { name: '저장' }).click()
  // 저장하면 왼쪽 목록에 나타난다.
  await expect(dialog.locator('aside').getByRole('button', { name: /매출표/ })).toBeVisible()
  await dialog.getByRole('button', { name: '표 닫기' }).click()

  // 열 이름으로 가리킨다.
  await write([{row:5,column:1,formula:'=SUM(매출표[금액])'},{row:6,column:1,formula:'=COUNTA(매출표[지역])'}])
  await expect.poll(()=>values('A5:A6'),{timeout:15_000}).toEqual([300,2])

  // 위에 행을 끼워도 그 수식은 그대로 맞아야 한다. 범위로 적었으면 사람이
  // 옮겨 적어야 하는 자리다.
  const current = await page.request.get(`/api/v1/workbooks/${workbookId}`).then(response => response.json())
  const inserted = await page.request.patch(`/api/v1/sheets/${sheetId}/structure:apply`, {
    data: { base_version: current.version, idempotency_key: `e2e-insert-${Date.now()}`, axis: 'row', action: 'insert', index: 1, count: 1 },
  })
  expect(inserted.status(), await inserted.text()).toBeLessThan(300)
  await expect.poll(()=>values('A6:A7'),{timeout:15_000}).toEqual([300,2])
})
