import { describe, expect, it } from 'vitest'
import { looksLikeNumberStoredAsText, spreadsheetNumber, textToNumber } from './spreadsheetNumber'

// 셈하는 규칙이 세 곳에 따로 적혀 있었다. 선택 요약과 열 통계와 수식 엔진이
// 셋 다 달라, 같은 열에서 세 가지 합계가 나올 수 있었다. 이제 한 곳에서
// 나오되, 그 한 곳은 엔진과 같아야 한다.
describe('spreadsheetNumber',()=>{
  it('엔진과 같은 것만 센다',()=>{
    expect(spreadsheetNumber(1234)).toBe(1234)
    expect(spreadsheetNumber('2000')).toBe(2000)
    expect(spreadsheetNumber(' 300 ')).toBe(300)
    expect(spreadsheetNumber(true)).toBe(1)
    expect(spreadsheetNumber(false)).toBe(0)
  })
  it('엔진이 글자로 보는 것은 세지 않는다',()=>{
    // 쉼표가 붙은 것을 세면 화면에는 더해지는데 수식에서는 빠져, 두 숫자가
    // 어긋난 채로 남는다.
    for(const value of ['1,234','50%','1234원','(500)','','   ',null,undefined,{}]){
      expect(spreadsheetNumber(value)).toBeUndefined()
    }
  })
})

describe('textToNumber',()=>{
  it('사람이 적는 꼴을 숫자로 바꾼다',()=>{
    expect(textToNumber('1,234')).toBe(1234)
    expect(textToNumber('1,234,567')).toBe(1234567)
    expect(textToNumber('₩1,234')).toBe(1234)
    expect(textToNumber('1,234원')).toBe(1234)
    expect(textToNumber('-1,234')).toBe(-1234)
    // 괄호는 회계에서 음수를 적는 꼴이다.
    expect(textToNumber('(500)')).toBe(-500)
    expect(textToNumber('(1,500)')).toBe(-1500)
    // 백분율은 100 으로 나눈다. 화면에 보이던 것과 셈하는 값이 같아야 한다.
    expect(textToNumber('50%')).toBe(0.5)
    expect(textToNumber('1,234.56')).toBe(1234.56)
  })
  it('숫자가 아닌 것을 억지로 만들지 않는다',()=>{
    // 쉼표가 아무 데나 찍힌 것은 숫자가 아니다. 억지로 바꾸면 사람이 적은
    // 것과 다른 값이 칸에 들어간다.
    for(const value of ['1,2,3','12,34','아무개','1234abc','','--5','1.2.3']){
      expect(textToNumber(value)).toBeUndefined()
    }
  })
})

describe('looksLikeNumberStoredAsText',()=>{
  it('이미 셈에 들어가는 것은 고칠 것이 없다',()=>{
    for(const value of ['2000',' 300 ',1234,true,null]){
      expect(looksLikeNumberStoredAsText(value)).toBe(false)
    }
  })
  it('사람 눈에는 숫자인데 수식이 빼고 세는 것을 찾는다',()=>{
    for(const value of ['1,234','₩1,234','1,234원','(500)','50%']){
      expect(looksLikeNumberStoredAsText(value)).toBe(true)
    }
  })
  it('숫자가 아닌 글자는 건드리지 않는다',()=>{
    for(const value of ['아무개','','1,2,3']){
      expect(looksLikeNumberStoredAsText(value)).toBe(false)
    }
  })
})
