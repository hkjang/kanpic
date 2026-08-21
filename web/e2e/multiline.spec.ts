import { expect, test, type Page } from '@playwright/test'

/**
 * Emulates what a Korean IME does in Chrome: a composition that updates as
 * jamo arrive and is confirmed with Enter. The confirming Enter arrives while
 * isComposing is true, which is exactly the case that used to swallow input.
 */
async function composeHangul(page:Page,label:string,syllables:string[]){
  await page.getByLabel(label).evaluate((element,values)=>{
    const field=element as HTMLTextAreaElement
    // React tracks the value it set, so the native setter is what makes a
    // programmatic change look like typing to it.
    const setValue=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value')!.set!
    field.focus()
    for(const syllable of values){
      field.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}))
      setValue.call(field,field.value+syllable)
      field.dispatchEvent(new InputEvent('input',{bubbles:true,data:syllable,isComposing:true}))
      field.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true,data:syllable}))
      field.dispatchEvent(new InputEvent('input',{bubbles:true,data:syllable}))
    }
  },syllables)
}

// A second line inside one cell is written with Alt+Enter, the way it is in
// every other spreadsheet.
test('Alt+Enter writes a line break and Enter still commits', async ({ page, request }) => {
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`줄바꿈 ${Date.now()}`}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id
  await page.goto(`/workbooks/${workbook.id}`)
  await page.waitForSelector('.grid-canvas')

  const editor=page.getByLabel('A1 셀 입력')
  await editor.fill('첫 줄')
  await editor.press('Alt+Enter')
  await editor.type('둘째 줄')
  await expect(editor).toHaveValue('첫 줄\n둘째 줄')
  // Enter without Alt still finishes the cell and moves down.
  await editor.press('Enter')
  await expect.poll(async()=>{
    const items=(await (await request.get(`/api/v1/sheets/${sheet}/ranges/A1:A1`)).json()).items
    return items[0]?.value
  }).toBe('첫 줄\n둘째 줄')
  await expect(page.getByLabel('A2 셀 입력')).toBeVisible()
})

// The editor is a text area now, so the composition path is checked again:
// the jamo must survive and the confirming Enter must not leave a stray line.
test('Hangul input still commits cleanly in the multi-line editor', async ({ page, request }) => {
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`한글 입력 ${Date.now()}`}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id
  await page.goto(`/workbooks/${workbook.id}`)
  await page.waitForSelector('.grid-canvas')

  // F2 opens the cell for editing, which is where an IME composition lands.
  await page.getByLabel('A1 셀 입력').press('F2')
  await composeHangul(page,'A1 셀 입력',['안','녕'])
  await expect(page.getByLabel('A1 셀 입력')).toHaveValue(/안녕$/)
  await page.getByLabel('A1 셀 입력').press('Enter')
  await expect.poll(async()=>{
    const items=(await (await request.get(`/api/v1/sheets/${sheet}/ranges/A1:A1`)).json()).items
    return items[0]?.value
  }).toMatch(/안녕$/)
  // No stray newline came from confirming the composition.
  const stored=(await (await request.get(`/api/v1/sheets/${sheet}/ranges/A1:A1`)).json()).items[0].value
  expect(String(stored)).not.toContain('\n')
})

// Editing a multi-line cell from the formula bar must not quietly flatten it.
test('the formula bar keeps the line breaks of a multi-line cell', async ({ page, request }) => {
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`수식 입력창 ${Date.now()}`}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id
  await request.patch(`/api/v1/sheets/${sheet}/cells:batch`,{data:{idempotency_key:`seed-${Date.now()}`,cells:[
    {row:1,column:1,value:'첫 줄\n둘째 줄'},
  ]}})
  await page.goto(`/workbooks/${workbook.id}`)
  await page.waitForSelector('.grid-canvas')

  const bar=page.getByLabel('수식 입력창')
  await expect(bar).toHaveValue('첫 줄\n둘째 줄')
  await bar.click()
  // End stops at the end of the line it is on, so the caret goes to the very end.
  await bar.press('Control+End')
  await bar.type('!')
  await bar.press('Enter')
  await expect.poll(async()=>{
    const items=(await (await request.get(`/api/v1/sheets/${sheet}/ranges/A1:A1`)).json()).items
    return items[0]?.value
  }).toBe('첫 줄\n둘째 줄!')
})
