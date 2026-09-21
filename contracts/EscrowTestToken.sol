// SPDX-License-Identifier: MIT
pragma solidity 0.8.37;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";

/// @notice Local simulated-EVM fixture only. Never deploy as a real USDC token.
contract EscrowTestToken is ERC20 {
    uint8 public mode;
    address public callback;
    bytes public callbackData;
    bool public callbackSucceeded;
    bytes public callbackResult;

    constructor() ERC20("Local test USDC", "USDC") {}

    function decimals() public pure override returns (uint8) {
        return 6;
    }

    function mint(address account, uint256 amount) external {
        _mint(account, amount);
    }

    function configure(uint8 nextMode, address target, bytes calldata data) external {
        mode = nextMode;
        callback = target;
        callbackData = data;
    }
    function transfer(address to, uint256 amount) public override returns (bool) {
        if (mode == 1) {
            return false;
        }
        if (mode == 2) {
            revert("fixture rejected transfer");
        }
        if (mode == 3) {
            _transfer(msg.sender, to, amount - 1);
            return true;
        }
        if (mode == 4) {
            (callbackSucceeded, callbackResult) = callback.call(callbackData);
        }
        return super.transfer(to, amount);
    }
    function transferFrom(address from, address to, uint256 amount) public override returns (bool) {
        if (mode == 1) {
            return false;
        }
        if (mode == 2) {
            revert("fixture rejected transfer");
        }
        if (mode == 3) {
            _spendAllowance(from, msg.sender, amount);
            _transfer(from, to, amount - 1);
            return true;
        }
        if (mode == 4) {
            (callbackSucceeded, callbackResult) = callback.call(callbackData);
        }
        return super.transferFrom(from, to, amount);
    }
}
