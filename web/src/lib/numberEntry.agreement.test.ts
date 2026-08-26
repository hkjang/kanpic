import { describe,expect,it } from 'vitest'
import { parsePastedNumber } from './clipboardNumber'
import { looksLikeNumberStoredAsText, numberFormatForText, textToNumber } from './spreadsheetNumber'
import { formatCellValue } from './cellFormat'

/**
 * 같은 글자가 스프레드시트에 들어오는 길은 둘이다 — 붙여넣기와,
 * 이미 글자로 들어와 있는 것을 데이터 정리로 고치는 것.
 *
 * 두 길이 다른 값이나 다른 서식을 내면, 같은 "₩5,000" 이 어떻게 들어왔는지에
 * 따라 다르게 저장된다. 사람에게는 같은 자료인데 파일에서는 다르다.
 */
const both=['₩5,000','1,234','(500)','50%','12.5%','$1,234.50','1,234원','£5,000','(1,234)','₩1,000.50']

describe('the two ways a number enters agree',()=>{
  it('gives the same value and the same format',()=>{
    for(const text of both){
      const pasted=parsePastedNumber(text)
      expect(pasted,`붙여넣기가 ${text} 를 숫자로 읽어야 한다`).toBeDefined()
      expect(looksLikeNumberStoredAsText(text),`정리가 ${text} 를 찾아야 한다`).toBe(true)
      expect(textToNumber(text),text).toBe(pasted!.value)
      expect(numberFormatForText(text),text).toBe(pasted!.numberFormat)
    }
  })

  it('draws what the person was looking at',()=>{
    for(const text of both){
      const pasted=parsePastedNumber(text)!
      expect(formatCellValue(pasted.value,{number_format:pasted.numberFormat}),text).toBe(text)
    }
  })

  it('refuses a number too long to hold, on both paths',()=>{
    // 스무 자리는 계좌번호이지 금액이 아니다. 숫자로 바꾸면 뒤가 뭉개져
    // 붙여넣은 사람이 보던 것과 다른 값이 칸에 들어간다.
    for(const text of ['12345678901234567890','₩12345678901234567890','12,345,678,901,234,567,890']){
      expect(parsePastedNumber(text),text).toBeUndefined()
      expect(textToNumber(text),text).toBeUndefined()
    }
  })
})
