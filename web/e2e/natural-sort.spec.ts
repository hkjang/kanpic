import { expect, test } from '@playwright/test'

// 월 이름을 글자 그대로 정렬하면 10월과 12월이 1월보다 앞에 온다. 그렇게
// 정렬하고 싶은 사람은 없다.
test('sorting month names puts them in month order, and the dialog can turn that off', async ({ page, request }) => {
  const stamp=Date.now()
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`정렬 ${stamp}`}}).then(response=>response.json())
  const sheet=workbook.sheets[0].id as string
  await request.patch(`/api/v1/sheets/${sheet}/cells:batch`,{data:{idempotency_key:`sort-${stamp}`,cells:[
    {row:1,column:1,value:'2월'},{row:2,column:1,value:'10월'},{row:3,column:1,value:'1월'},
    {row:4,column:1,value:'12월'},{row:5,column:1,value:'3월'},
  ]}})
  await page.goto(`/workbooks/${workbook.id}`)
  await expect(page.locator('.grid-canvas')).toBeVisible()
  const months=async()=>(await request.get(`/api/v1/sheets/${sheet}/ranges/A1:A5`).then(r=>r.json())).items.map((cell:{value:unknown})=>cell.value)

  await page.getByRole('combobox',{name:'이름 상자'}).fill('A1:A5')
  await page.getByRole('combobox',{name:'이름 상자'}).press('Enter')
  await page.getByRole('button',{name:'범위 정렬'}).click()
  const dialog=page.getByRole('dialog',{name:'범위 정렬'})
  await expect(dialog).toBeVisible()
  await dialog.getByRole('checkbox',{name:'첫 행을 헤더로 고정'}).uncheck()
  await dialog.getByRole('button',{name:'정렬 적용'}).click()
  await expect.poll(months).toEqual(['1월','2월','3월','10월','12월'])

  // 글자 그대로 견주고 싶은 사람은 끌 수 있어야 한다.
  await page.getByRole('combobox',{name:'이름 상자'}).fill('A1:A5')
  await page.getByRole('combobox',{name:'이름 상자'}).press('Enter')
  await page.getByRole('button',{name:'범위 정렬'}).click()
  await expect(dialog).toBeVisible()
  await dialog.getByRole('checkbox',{name:'첫 행을 헤더로 고정'}).uncheck()
  await dialog.getByRole('checkbox',{name:'숫자를 글자로 취급'}).check()
  await dialog.getByRole('button',{name:'정렬 적용'}).click()
  await expect.poll(months).toEqual(['10월','12월','1월','2월','3월'])
  await request.delete(`/api/v1/workbooks/${workbook.id}`)
})
