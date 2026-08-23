import { expect, test } from '@playwright/test'
test('autofit after hashes', async ({ page, request }) => {
  const stamp=Date.now()
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`맞춤 ${stamp}`}}).then(r=>r.json())
  const sheet=workbook.sheets[0].id as string
  await request.patch(`/api/v1/sheets/${sheet}/cells:batch`,{data:{idempotency_key:`a-${stamp}`,cells:[
    {row:1,column:1,value:123456789012345},{row:1,column:2,value:'막음'},
  ]}})
  const created=await request.get(`/api/v1/workbooks/${workbook.id}`).then(r=>r.json())
  await request.patch(`/api/v1/sheets/${sheet}/layout:apply`,{headers:{'Idempotency-Key':`s-${stamp}`},data:{expected_revision:created.sheets[0].layout.revision,action:'resize',axis:'column',start:1,count:1,size:70}})
  await page.goto(`/workbooks/${workbook.id}`)
  await expect(page.locator('.grid-canvas')).toBeVisible()
  await page.waitForTimeout(800)
  const box=(await page.locator('.grid-canvas').boundingBox())!
  // A열 오른쪽 경계를 머리글에서 두 번 클릭한다.
  await page.mouse.dblclick(box.x+46+70,box.y+13)
  await page.waitForTimeout(1200)
  const after=await request.get(`/api/v1/workbooks/${workbook.id}`).then(r=>r.json())
  console.log('widths',JSON.stringify(after.sheets[0].layout.column_widths))
  await request.delete(`/api/v1/workbooks/${workbook.id}`)
})
