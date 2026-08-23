import {describe,expect,it} from 'vitest'
import {formatCellValue,wrapText} from './cellFormat'

describe('cell display formats',()=>{
  it('formats grouped numbers, percentages, currencies and scientific values',()=>{
    expect(formatCellValue(1234.5,{number_format:'#,##0.00'},'en-US')).toBe('1,234.50')
    expect(formatCellValue(.125,{number_format:'0.0%'},'en-US')).toBe('12.5%')
    expect(formatCellValue(2500,{number_format:'₩#,##0'},'en-US')).toBe('₩2,500')
    expect(formatCellValue(1234,{number_format:'0.00E+00'},'en-US')).toBe('1.23E+3')
    expect(formatCellValue(42,{number_format:'00000'},'en-US')).toBe('00042')
  })
  it('formats Excel serial dates and times without timezone drift',()=>{
    expect(formatCellValue(45292,{number_format:'yyyy-mm-dd'},'en-CA')).toBe('2024-01-01')
    expect(formatCellValue(.5,{number_format:'hh:mm'},'en-GB')).toBe('12:00')
    expect(formatCellValue(.75,{number_format:'h:mm AM/PM'},'en-US')).toBe('6:00 PM')
    expect(formatCellValue(1234,{number_format:'#,##0 ;[red](#,##0)'},'en-US')).toBe('1,234')
  })
  it('wraps words and long unbroken text',()=>{
    expect(wrapText('one two three',7,text=>text.length)).toEqual(['one two','three'])
    expect(wrapText('abcdefgh',3,text=>text.length)).toEqual(['abc','def','gh'])
  })
})

// 격자에 보이는 자릿수는 사람이 적은 십진수를 따라야 한다. 1.005 는 이진
// 실수로 1.00499999999999989… 로 담기지만 엑셀과 시트는 1.01 로 보여준다.
// Intl.NumberFormat 은 가장 짧은 십진수 표기를 기준으로 반올림하므로 이미
// 맞게 나온다 — (1.005).toFixed(2) 는 "1.00" 이라 쓸 수 없다.
//
// 서버의 TEXT 는 Go 의 FormatFloat 에 그대로 맡기고 있어서 "1.00" 을 냈다.
// 격자와 TEXT 가 다른 답을 내고 있었던 것이다. 아래 값은 서버의
// internal/formula/library_extended_test.go 와 **같은 값** 을 고정한다.
describe('the grid rounds the way the server does',()=>{
  it('rounds the decimal people typed, not the binary float',()=>{
    expect(formatCellValue(1.005,{number_format:'0.00'},'en-US')).toBe('1.01')
    expect(formatCellValue(2.675,{number_format:'0.00'},'en-US')).toBe('2.68')
    expect(formatCellValue(8.475,{number_format:'0.00'},'en-US')).toBe('8.48')
    expect(formatCellValue(1234.5,{number_format:'#,##0.00'},'en-US')).toBe('1,234.50')
    // toFixed 를 쓰면 아래가 "1.00" 이 된다. 쓰지 않는 이유다.
    expect((1.005).toFixed(2)).toBe('1.00')
  })
})

// 엑셀 파일에서 읽어 온 날짜는 1899-12-30부터 센 날 수로 담긴다. 격자는
// 이 번호를 날짜로 보여주어야 하고, 서버의 수식도 같은 날짜로 읽어야 한다.
// 아래 값은 서버의 internal/formula/library_extended_test.go 와 **같은
// 날짜** 를 고정한다. 한쪽만 고치면 양쪽 다 걸린다.
describe('the grid reads the date serials an import brings in',()=>{
  it('turns a serial into the same day the server does',()=>{
    expect(formatCellValue(45306,{number_format:'yyyy-mm-dd'},'en-US')).toBe('2024-01-15')
    // 엑셀은 1900년을 윤년으로 잘못 센다. 60번은 없는 날이라, 그보다 작은
    // 번호는 하루 뒤에서 세야 1번이 1900-01-01이 된다.
    expect(formatCellValue(1,{number_format:'yyyy-mm-dd'},'en-US')).toBe('1900-01-01')
    expect(formatCellValue(59,{number_format:'yyyy-mm-dd'},'en-US')).toBe('1900-02-28')
    expect(formatCellValue(61,{number_format:'yyyy-mm-dd'},'en-US')).toBe('1900-03-01')
  })
})
