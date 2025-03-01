#include "textflag.h"

// func maskXORSIMDAsm(b []byte, key [4]byte, pos int) int
TEXT ·maskXORSIMDAsm(SB), NOSPLIT, $0
    MOVQ b+0(FP), DI    // DI = &b[0]
    MOVQ $0, SI         // SI = i = 0
    MOVQ b_len+8(FP), BX // BX = len(b)
    CMPQ BX, $16        // if len(b) < 16, use scalar code
    JL scalar

    // 加载key到XMM寄存器
    MOVQ key+24(FP), X0
    PSHUFB X15, X0      // 将key复制到XMM0的所有字节
    MOVO X0, X1         // 复制key到X1
    MOVO X0, X2         // 复制key到X2
    MOVO X0, X3         // 复制key到X3

    // 对齐到16字节边界
    MOVQ DI, AX
    ANDQ $15, AX        // AX = DI % 16
    JZ aligned

    // 处理未对齐的字节
    MOVQ $16, CX
    SUBQ AX, CX         // CX = 16 - (DI % 16) = 需要处理的字节数
    CMPQ CX, BX         // 如果剩余字节数小于需要处理的字节数
    JGE scalar          // 跳转到标量代码

    // 处理未对齐的部分
    SUBQ CX, BX         // BX = BX - CX
    ADDQ CX, SI         // SI = SI + CX

aligned:
    // 每次处理64字节
    CMPQ BX, $64
    JL tail

    // 主循环 - 每次处理64字节
loop64:
    // 加载数据
    MOVOU (DI), X4
    MOVOU 16(DI), X5
    MOVOU 32(DI), X6
    MOVOU 48(DI), X7

    // 异或操作
    PXOR X0, X4
    PXOR X1, X5
    PXOR X2, X6
    PXOR X3, X7

    // 存储结果
    MOVOU X4, (DI)
    MOVOU X5, 16(DI)
    MOVOU X6, 32(DI)
    MOVOU X7, 48(DI)

    // 更新指针和计数器
    ADDQ $64, DI
    ADDQ $64, SI
    SUBQ $64, BX
    CMPQ BX, $64
    JGE loop64

tail:
    // 处理剩余的16字节块
    CMPQ BX, $16
    JL scalar

loop16:
    // 加载数据
    MOVOU (DI), X4

    // 异或操作
    PXOR X0, X4

    // 存储结果
    MOVOU X4, (DI)

    // 更新指针和计数器
    ADDQ $16, DI
    ADDQ $16, SI
    SUBQ $16, BX
    CMPQ BX, $16
    JGE loop16

scalar:
    // 处理剩余的字节
    CMPQ BX, $0
    JE done

    // 返回已处理的字节数
    MOVQ SI, ret+48(FP)
    RET

done:
    // 返回0，表示所有字节都已处理
    MOVQ $0, ret+48(FP)
    RET