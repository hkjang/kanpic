import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

/**
 * Ctrl+arrow and Ctrl+A on a sheet longer than the screen. The grid holds only
 * the rows it shows, so both used to decide from those: Ctrl+Down stopped where
 * the loaded rows ended, in the middle of a column with no gap, and Ctrl+A
 * froze the page summing three billion empty addresses.
 */
const ROWS=3000

async function tallTable(request:APIRequestContext,title:string){
  const workbook=await request.post('/api/v1/workbooks',{data:{title}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id
  const cells:Array<{row:number;column:number;value:unknown}>=[]
  for(let row=1;row<=ROWS;row+=1)cells.push({row,column:1,value:`항목 ${row}`},{row,column:2,value:row})
  const response=await request.patch(`/api/v1/sheets/${sheet}/cells:paste`,{data:{idempotency_key:`edge-seed-${Date.now()}`,cells}})
  expect(response.ok()).toBeTruthy()
  return {workbook,sheet}
}

async function openAt(page:Page,workbookId:string,address:string){
  await page.goto(`/workbooks/${workbookId}`)
  await page.waitForSelector('.grid-canvas')
  await page.locator('.name-box').fill(address)
  await page.locator('.name-box').press('Enter')
  await expect(page.locator('.name-box')).toHaveValue(address)
  await page.getByLabel(`${address} 셀 입력`).focus()
}

test('Ctrl+Down runs to the last row of a column longer than the screen', async ({ page, request }) => {
  const {workbook}=await tallTable(request,`데이터 끝 ${Date.now()}`)
  await openAt(page,workbook.id,'A1')
  await page.keyboard.press('Control+ArrowDown')
  await expect(page.locator('.name-box')).toHaveValue(`A${ROWS}`)
  // Below the last row there is nothing, so the next press goes to the edge.
  await page.keyboard.press('Control+ArrowUp')
  await expect(page.locator('.name-box')).toHaveValue('A1')
  // Two presses in a row: the second starts where the first landed.
  await page.keyboard.press('Control+ArrowDown')
  await page.keyboard.press('Control+ArrowUp')
  await expect(page.locator('.name-box')).toHaveValue('A1')
  // Extending with Shift keeps the anchor and selects the whole column of data.
  await page.keyboard.press('Control+Shift+ArrowDown')
  await expect(page.locator('.name-box')).toHaveValue(`A1:A${ROWS}`)
})

test('Ctrl+A selects the table first and the whole sheet on the second press, without freezing', async ({ page, request }) => {
  const {workbook}=await tallTable(request,`전체 선택 ${Date.now()}`)
  await openAt(page,workbook.id,'B10')
  await page.keyboard.press('Control+a')
  await expect(page.locator('.name-box')).toHaveValue(`A1:B${ROWS}`)
  // The summary covers every selected row, not only the ones on screen.
  await expect(page.locator('.selection-summary')).toContainText(`합계 ${(ROWS*(ROWS+1)/2).toLocaleString('ko-KR')}`)

  await page.keyboard.press('Control+a')
  await expect(page.locator('.selection-summary')).toContainText('선택 3,276,800,000셀',{timeout:5_000})
  // The page still answers: a frozen tab would not run this.
  const answered=await page.evaluate(()=>new Promise<number>(resolve=>{const started=performance.now();requestAnimationFrame(()=>resolve(performance.now()-started))}))
  expect(answered).toBeLessThan(2_000)
})
