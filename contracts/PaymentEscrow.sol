// SPDX-License-Identifier: MIT
pragma solidity 0.8.37;

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

/// @notice Testnet-only, single-token escrow. Buyer releases; seller refunds.
/// There is no administrator, deadline, arbitration, upgrade or partial settlement.
contract PaymentEscrow is ReentrancyGuard {
    using SafeERC20 for IERC20;

    enum State {
        None,
        Funded,
        Released,
        Refunded
    }

    struct Order {
        address seller;
        uint256 amount;
        State state;
    }

    IERC20 public immutable token;
    uint256 public totalLocked;
    mapping(address buyer => mapping(bytes32 orderId => Order)) public orders;

    event Funded(address indexed buyer, bytes32 indexed orderId, address indexed seller, uint256 amount);
    event Released(address indexed buyer, bytes32 indexed orderId, address indexed seller, uint256 amount);
    event Refunded(address indexed buyer, bytes32 indexed orderId, address indexed seller, uint256 amount);

    error InvalidOrder();
    error OrderAlreadyExists();
    error OrderNotFunded();
    error Unauthorized();
    error UnsupportedTokenTransfer();

    constructor(IERC20 paymentToken) {
        if (address(paymentToken).code.length == 0) {
            revert InvalidOrder();
        }
        token = paymentToken;
    }

    function fund(bytes32 orderId, address seller, uint256 amount) external nonReentrant {
        if (
            orderId == bytes32(0) || seller == address(0) || seller == msg.sender ||
            seller == address(this) || amount == 0
        ) {
            revert InvalidOrder();
        }
        Order storage order = orders[msg.sender][orderId];
        if (order.state != State.None) {
            revert OrderAlreadyExists();
        }
        order.seller = seller;
        order.amount = amount;
        order.state = State.Funded;
        totalLocked += amount;
        uint256 beforeBalance = token.balanceOf(address(this));
        token.safeTransferFrom(msg.sender, address(this), amount);
        if (token.balanceOf(address(this)) != beforeBalance + amount) {
            revert UnsupportedTokenTransfer();
        }
        emit Funded(msg.sender, orderId, seller, amount);
    }

    function release(address buyer, bytes32 orderId) external nonReentrant {
        if (msg.sender != buyer) {
            revert Unauthorized();
        }
        Order storage order = orders[buyer][orderId];
        if (order.state != State.Funded) {
            revert OrderNotFunded();
        }
        order.state = State.Released;
        totalLocked -= order.amount;
        pay(order.seller, order.amount);
        emit Released(buyer, orderId, order.seller, order.amount);
    }

    function refund(address buyer, bytes32 orderId) external nonReentrant {
        Order storage order = orders[buyer][orderId];
        if (msg.sender != order.seller) {
            revert Unauthorized();
        }
        if (order.state != State.Funded) {
            revert OrderNotFunded();
        }
        order.state = State.Refunded;
        totalLocked -= order.amount;
        pay(buyer, order.amount);
        emit Refunded(buyer, orderId, order.seller, order.amount);
    }

    function pay(address recipient, uint256 amount) private {
        uint256 beforeBalance = token.balanceOf(recipient);
        uint256 beforeEscrow = token.balanceOf(address(this));
        token.safeTransfer(recipient, amount);
        if (
            token.balanceOf(recipient) != beforeBalance + amount ||
            token.balanceOf(address(this)) != beforeEscrow - amount
        ) {
            revert UnsupportedTokenTransfer();
        }
    }
}
