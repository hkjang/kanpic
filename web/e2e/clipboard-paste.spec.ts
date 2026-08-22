import { expect, test, type Page } from '@playwright/test'

// 다른 스프레드시트에서 복사하면 클립보드에는 평문과 HTML 이 함께 올라간다.
// 평문에는 **보이던 글자**만 담기므로 `₩1,234` 를 그대로 저장하면 합계가
// 0이 된다. 서식도 전부 사라졌다.
//
// 이 시나리오는 반드시 실제 브라우저에서 돌아야 한다. 이 앱의 CSP 에는
// `style-src 'unsafe-inline'` 이 없어 크롬이 style 속성을 CSSOM 에 넣지
// 않는다. jsdom 에는 CSP 가 없어 이 차이를 잡지 못한다.
const pasteInto=async(page:Page,html:string,text:string)=>{
  await page.evaluate(([html,text])=>{
    const data=new DataTransfer()
    data.setData('text/html',html)
    data.setData('text/plain',text)
    document.querySelector('.grid-viewport')?.dispatchEvent(new ClipboardEvent('paste',{clipboardData:data,bubbles:true,cancelable:true}))
  },[html,text])
}

const openAtA1=async(page:Page,workbookId:string)=>{
  await page.goto(`/workbooks/${workbookId}`)
  await expect(page.locator('.grid-canvas')).toBeVisible()
  const box=(await page.locator('.grid-canvas').boundingBox())!
  await page.mouse.click(box.x+40,box.y+42)
}

test('pasting a formatted range from Excel keeps its numbers and its formatting', async ({ page, request }) => {
  const stamp=Date.now()
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`엑셀 붙여넣기 ${stamp}`}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id as string
  await openAtA1(page,workbook.id)
  await pasteInto(page,
    '<html xmlns:x="urn:schemas-microsoft-com:office:excel"><body><table><tr>'
    +'<td style="font-weight:700;background:#DBEAFE;text-align:center">제품</td>'
    +'<td style="font-weight:700;background:#DBEAFE">단가</td></tr><tr>'
    +'<td>연필</td><td align=right x:num="1234.5" style="color:#B91C1C">₩1,234.50</td></tr><tr>'
    +'<td>공책</td><td align=right x:num="98000">₩98,000</td></tr></table></body></html>',
    '제품\t단가\n연필\t₩1,234.50\n공책\t₩98,000')

  await expect.poll(async()=>{
    const items=(await request.get(`/api/v1/sheets/${sheet}/ranges/A1:B3`).then(r=>r.json())).items
    return items.length
  }).toBe(6)
  const items=(await request.get(`/api/v1/sheets/${sheet}/ranges/A1:B3`).then(r=>r.json())).items
  const at=(row:number,column:number)=>items.find((cell:{row:number;column:number})=>cell.row===row&&cell.column===column)
  // 보이던 글자가 아니라 셀이 담고 있던 숫자로 들어와야 합계가 성립한다.
  expect(at(2,2)).toMatchObject({value:1234.5})
  expect(at(3,2)).toMatchObject({value:98000})
  expect(at(1,1)).toMatchObject({style:{bold:true,background:'#DBEAFE',horizontal_align:'center'}})
  expect(at(2,2)!.style).toMatchObject({color:'#B91C1C'})

  const current=await request.get(`/api/v1/workbooks/${workbook.id}`).then(r=>r.json())
  await request.patch(`/api/v1/sheets/${sheet}/cells:batch`,{data:{base_version:current.version,idempotency_key:`sum-${stamp}`,cells:[{row:5,column:2,formula:'=SUM(B2:B3)'}]}})
  const total=(await request.get(`/api/v1/sheets/${sheet}/ranges/B5:B5`).then(r=>r.json())).items[0]
  expect(total).toMatchObject({value:99234.5})
  await request.delete(`/api/v1/workbooks/${workbook.id}`)
})

test('pasting from Google Sheets uses the value it declares, not the text it shows', async ({ page, request }) => {
  const stamp=Date.now()
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`시트 붙여넣기 ${stamp}`}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id as string
  await openAtA1(page,workbook.id)
  await pasteInto(page,
    '<meta charset="utf-8"><table><tbody><tr>'
    +'<td data-sheets-value=\'{"1":2,"2":"분기"}\' style="font-style:italic">분기</td>'
    +'<td data-sheets-value=\'{"1":3,"3":0.125}\' style="text-align:right">12.5%</td></tr><tr>'
    +'<td colspan="2" style="background:rgb(254, 226, 226)">두 칸을 차지하는 셀</td></tr><tr>'
    +'<td>다음 줄</td><td>1,234</td></tr></tbody></table>',
    '분기\t12.5%\n두 칸을 차지하는 셀\t\n다음 줄\t1,234')

  await expect.poll(async()=>{
    const items=(await request.get(`/api/v1/sheets/${sheet}/ranges/A1:B3`).then(r=>r.json())).items
    return items.length
  }).toBe(5)
  const items=(await request.get(`/api/v1/sheets/${sheet}/ranges/A1:B3`).then(r=>r.json())).items
  const at=(row:number,column:number)=>items.find((cell:{row:number;column:number})=>cell.row===row&&cell.column===column)
  expect(at(1,1)).toMatchObject({value:'분기',style:{italic:true}})
  expect(at(1,2)).toMatchObject({value:0.125})
  expect(at(2,1)).toMatchObject({value:'두 칸을 차지하는 셀',style:{background:'#FEE2E2'}})
  // colspan 이 걸린 셀이 두 칸을 차지하므로 다음 줄이 왼쪽으로 밀리면 안 된다.
  expect(at(3,1)).toMatchObject({value:'다음 줄'})
  expect(at(3,2)).toMatchObject({value:1234,style:{number_format:'#,##0'}})
  await request.delete(`/api/v1/workbooks/${workbook.id}`)
})
