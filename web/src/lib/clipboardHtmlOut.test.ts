import { describe, expect, it } from 'vitest'
import type { KanpicClipboard } from './clipboard'
import { clipboardHtml } from './clipboardHtmlOut'
import { parseClipboardHtml } from './clipboardHtml'

const payload=(cells:KanpicClipboard['cells'],rows=1,columns=1):KanpicClipboard=>(
  {version:1,sourceRow:1,sourceColumn:1,rows,columns,cells})

describe('clipboardHtml',()=>{
  it('carries the formatting a plain-text copy loses',()=>{
    const html=clipboardHtml(payload([
      {rowOffset:0,columnOffset:0,value:'제품',style:{bold:true,background:'#DBEAFE',horizontal_align:'center'}},
      {rowOffset:0,columnOffset:1,value:1234.5,style:{color:'#B91C1C',number_format:'#,##0.00'}},
    ],1,2))
    expect(html).toContain('font-weight:700')
    expect(html).toContain('background-color:#DBEAFE')
    expect(html).toContain('text-align:center')
    expect(html).toContain('color:#B91C1C')
    // 엑셀은 붙여넣을 때 이 표시 형식을 셀에 적용한다.
    expect(html).toContain("mso-number-format:'#,##0.00'")
    // 보이던 글자와 함께 실제 숫자도 넘긴다.
    expect(html).toContain('x:num="1234.5"')
    expect(html).toContain('1,234.50')
  })

  it('escapes text that would otherwise become markup',()=>{
    const html=clipboardHtml(payload([{rowOffset:0,columnOffset:0,value:'<b>주의</b> & "인용"'}]))
    expect(html).toContain('&lt;b&gt;주의&lt;/b&gt; &amp; &quot;인용&quot;')
    expect(html).not.toContain('<b>주의')
  })

  it('leaves a format Excel could not read out of the declaration list',()=>{
    // 세미콜론이 들어간 조건부 표시 형식은 CSS 선언을 끊어 버린다.
    const html=clipboardHtml(payload([{rowOffset:0,columnOffset:0,value:5,style:{number_format:'#,##0;[Red]-#,##0'}}]))
    expect(html).not.toContain('mso-number-format')
    expect(html).toContain('x:num="5"')
  })

  it('survives its own round trip through the reader',()=>{
    const html=clipboardHtml(payload([
      {rowOffset:0,columnOffset:0,value:'합계',style:{bold:true,italic:true,horizontal_align:'right'}},
      {rowOffset:0,columnOffset:1,value:98000,style:{number_format:'#,##0"원"'}},
    ],1,2))
    const read=parseClipboardHtml(html,100)!
    expect(read[0]).toMatchObject({value:'합계',style:{bold:true,italic:true,horizontal_align:'right'}})
    expect(read[1]).toMatchObject({value:98000})
  })
})
